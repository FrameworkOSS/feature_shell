package shell

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/FrameworkOSS/portal/features/commands"
	"github.com/FrameworkOSS/portal/portal"
	"github.com/fatih/color"
)

var (
	cyan   = color.New(color.FgCyan).SprintFunc()
	green  = color.New(color.FgGreen).SprintFunc()
	red    = color.New(color.FgRed).SprintFunc()
	yellow = color.New(color.FgYellow).SprintFunc()
)

type Shell struct {
	lockResp  sync.Mutex
	resps     []*portal.Event
	processor *portal.EventHandler

	running      bool
	canType      bool
	prompted     bool
	lastPrompted bool
	canPrompt    bool

	press    bool
	instance string
	workdir  string
	initCmds []string

	p *portal.Portal
	c *commands.Commands
}

func NewShell(p *portal.Portal, instance string, requireStart, stdCmds bool, initCmds ...string) (sh *Shell) {
	if p == nil {
		panic("shell: portal is nil")
	}

	sh = new(Shell)
	sh.p = p
	sh.resps = make([]*portal.Event, 0)

	sh.processor = portal.NewEventHandler().
		Handle(sh.eWorkdir, "workdir").
		Handle(sh.eSuccess, "success").
		Handle(sh.eResp, "resp", "shell").
		Handle(sh.eError, "error")

	if instance == "" {
		instance = "shell"
	}
	sh.instance = instance
	sh.press = requireStart
	sh.initCmds = initCmds

	if stdCmds {
		sh.c = commands.NewCommands(p)
		p.FeatureAdd(sh.c)
	}

	return
}

func (sh *Shell) eWorkdir(e *portal.Event) error {
	wd := sh.workdir
	sh.workdir = string(e.GetData())
	if wd != sh.workdir {
		sh.lastPrompted = false
	}
	sh.prompt(true)
	return nil
}

func (sh *Shell) eSuccess(e *portal.Event) error {
	fmt.Printf("Success: %s (%s)\n", e.GetProducer(), e.GetChannel())
	sh.lastPrompted = false
	sh.prompt(true)
	return nil
}

func (sh *Shell) eResp(e *portal.Event) error {
	if e.GetDataSize() > 0 {
		str := string(e.GetData())
		if e.GetID() != "shell" && str[len(str)-1] != '\n' {
			str += "\n"
		}
		fmt.Printf("%s", str)
		sh.lastPrompted = false
	}
	sh.prompt(true)
	return nil
}

func (sh *Shell) eError(e *portal.Event) error {
	if e.GetDataSize() > 0 {
		fmt.Printf("%s\n", red(string(e.GetData())))
	} else {
		fmt.Println(red(fmt.Sprintf("Error: %s", e.GetProducer())))
	}
	sh.lastPrompted = false
	sh.prompt(true)
	return nil
}

func (sh *Shell) API() int {
	return 0
}

func (sh *Shell) ID() string {
	return "shell"
}

func (sh *Shell) Name() string {
	return "Shell"
}

func (sh *Shell) Authors() []string {
	return []string{"JoshuaDoes"}
}

func (sh *Shell) Description() string {
	return "An interactive shell to translate stdin to commands."
}

func (sh *Shell) Version() string {
	return "v0.0.1"
}

func (sh *Shell) Open() error {
	if sh.running {
		return fmt.Errorf("%s: already open", sh.ID())
	}
	go sh.loopStdin()
	return nil
}

func (sh *Shell) Close() (errs []error, retry bool) {
	sh.running = false
	return
}

func (sh *Shell) Input(e *portal.Event) error {
	sh.processor.Process(e)
	return nil
}

func (sh *Shell) Output() (*portal.Event, error) {
	sh.lockResp.Lock()
	defer sh.lockResp.Unlock()
	if len(sh.resps) == 0 {
		return nil, nil
	}
	e := sh.resps[0]
	sh.resps = sh.resps[1:]
	return e, nil
}

func (sh *Shell) storeResp(e *portal.Event) {
	sh.lockResp.Lock()
	defer sh.lockResp.Unlock()
	sh.resps = append(sh.resps, e)
}

func (sh *Shell) ready() {
	sh.running = true
	sh.storeResp(portal.NewEventReady(sh.ID(), true))
}

func (sh *Shell) unready() {
	sh.running = false
	sh.storeResp(portal.NewEventReady(sh.ID(), false))
}

func (sh *Shell) prompt(reset bool) {
	prompted := sh.prompted
	if reset {
		prompted = false
		sh.prompted = false
	}
	if !prompted {
		if !sh.lastPrompted && sh.canPrompt {
			prompt := fmt.Sprintf("%s%s%s:%s$ ", green(sh.ID()), yellow("@"), green(sh.p.ID()), cyan(filepath.Base(sh.workdir)))
			fmt.Fprintf(color.Output, "%s", prompt)
		}
		sh.lastPrompted = true
		sh.prompted = true
	}
	sh.canType = true
}

func (sh *Shell) loopStdin() {
	sh.ready()
	reader := bufio.NewReader(os.Stdin)

	if sh.press {
		fmt.Println("Press enter to start the shell. Press CTRL+D to stop the shell.")
		_, err := reader.ReadString('\n')
		if err != nil {
			sh.unready()
			return
		}
	}

	//Wait for a response about the current workdir from files
	sh.storeResp(portal.NewEvent().SetID("workdir").AddParticipants("files"))
	for {
		if !sh.running {
			return
		}
		if sh.canType {
			break
		}
	}

	//Start off by executing input commands!
	if len(sh.initCmds) > 0 {
		for i := 0; i < len(sh.initCmds); i++ {
			if err := sh.exec(sh.initCmds[i]); err != nil {
				sh.Input(portal.NewEventError(sh.ID(), err))
			}
			if !sh.running {
				return
			}
		}
	}

	sh.lastPrompted = false
	sh.canPrompt = true
	sh.prompt(true)

	for {
		for {
			if !sh.running {
				return
			}
			if sh.canType {
				break
			}
		}
		sh.prompt(false)

		line, err := reader.ReadString('\n')
		if err != nil {
			sh.running = false
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			sh.lastPrompted = false
			sh.prompt(true)
			continue
		}

		if err := sh.exec(line); err != nil {
			sh.Input(portal.NewEventError(sh.ID(), err))
		}
	}

	sh.unready()
	fmt.Println("")
}

func (sh *Shell) exec(cmd string) error {
	sh.canType = false
	op := strings.Split(cmd, " ")
	if !sh.handle(op...) {
		e, err := sh.c.NewCommandLineEvent(sh.ID(), op...)
		if err != nil {
			return err
		}
		sh.storeResp(e)
	}
	for {
		if !sh.running || sh.canType {
			break
		}
	}
	return nil
}

func (sh *Shell) handle(op ...string) bool {
	resp := ""

	cmd := strings.ToLower(op[0])
	switch cmd {
	// --- Add custom cases here! ---
	case "clear", "cls", "c", "reset":
		resp = "\033[H\033[2J"
	case "echo":
		resp = "\n"
		if len(op) > 1 {
			resp = strings.Join(op[1:], " ") + "\n"
		}
	case "sleep", "sleepms", "sleepmilli", "wait", "waitms", "waitmilli":
		if len(op) > 1 {
			duration, err := strconv.Atoi(op[1])
			if err != nil {
				sh.Input(portal.NewEventError(sh.ID(), err))
			}
			time.Sleep(time.Millisecond * time.Duration(duration))
		} else {
			time.Sleep(time.Second * 1)
		}
		resp = " "
	case "dump":
		resp = fmt.Sprintf("Features:\n-%s\n", strings.Join(sh.p.Features(), "\n-"))
	}

	if resp != "" {
		r := portal.NewEventResponse(sh.ID(), nil).SetID("shell")
		if resp != " " {
			r.SetData([]byte(resp))
		}
		sh.Input(r)
		return true
	}
	return false
}
