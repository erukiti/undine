package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/user"
	"syscall"
	"time"

	"github.com/erukiti/go-msgpack"
	"github.com/erukiti/go-util"
)

const (
	ErrorCodeNotFound = "exec not found"
	ErrorCodeOther    = "other"
	ErrorCodeFatal    = "fatal"
)

type Command struct {
	Type    string   `msgpack:"type"`
	UUID    string   `msgpack:"uuid"`
	Command string   `msgpack:"command"`
	Args    []string `msgpack:"args"`
}

type RequestReport struct {
	Type string `msgpack:"type"`
	UUID string `msgpack:"uuid"`
}

type RequestChdir struct {
	Type string `msgpack:"type"`
	UUID string `msgpack:"uuid"`
	Dir  string `msgpack:"dir"`
}

type Stdin struct {
	Type string `msgpack:"type"`
	UUID string `msgpack:"uuid"`
	Buf  []byte `msgpack:"buf"`
}

type Stdout struct {
	Type string `msgpack:"type"`
	UUID string `msgpack:"uuid"`
	Buf  []byte `msgpack:"buf"`
}

type Stderr struct {
	Type string `msgpack:"type"`
	UUID string `msgpack:"uuid"`
	Buf  []byte `msgpack:"buf"`
}

type Error struct {
	Type    string `msgpack:"type"`
	UUID    string `msgpack:"uuid"`
	Code    string `msgpack:"code"`
	Message string `msgpack:"message"`
}

type Exit struct {
	Type     string `msgpack:"type"`
	UUID     string `msgpack:"uuid"`
	Success  bool   `msgpack:"success"`
	Message  string `msgpack:"message"`
	SysTime  uint   `msgpack:"systime"`
	UserTime uint   `msgpack:"usertime"`
	ExitCode int    `msgpack:"code"`
}

type Report struct {
	Type     string `msgpack:"type"`
	UUID     string `msgpack:"uuid"`
	Username string `msgpack:"username"`
	Cwd      string `msgpack:"cwd"`
}

type ProcessReport struct {
	Pid int `msgpack:"pid"`
}

type Ping struct {
	Type string `msgpack:"type"`
}

func report(encoder msgpack.Encoder, uuid string) {
	usr, err := user.Current()
	if err != nil {
		fmt.Fprintf(os.Stderr, "user.Current error: %s\n", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Getwd error: %s\n", err)
	}

	report := Report{"report", uuid, usr.Username, cwd}
	err = encoder.Encode(report)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
	}
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	logFile := "./log.txt"
	if logFile != "" {
		logWriter, err := os.OpenFile(logFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
		if err != nil {
			log.Printf("log file error: %s\n", err)
		} else {
			log.SetOutput(logWriter)
		}
	}

	log.Printf("start %d", os.Getpid())

	commands := make(map[string]*Child)

	decoder := msgpack.NewDecoder(bufio.NewReader(os.Stdin))
	encoder := msgpack.NewEncoder(os.Stdout)

	chanPing := make(chan bool, 1)

	go func() {
		for {
			select {
			case <-chanPing:
				continue
			case <-time.After(10 * time.Second):
				log.Print("no ping timeout.")
				// os.Exit(127)
			}
		}
	}()

	go func() {
		for {
			ping := Ping{"ping"}
			encoder.Encode(ping)
			time.Sleep(5 * time.Second)
		}
	}()

	defer log.Printf("exit %d", os.Getpid())

	for {

		var com Command
		var stdin Stdin
		var requestReport RequestReport
		var requestChdir RequestChdir
		var ping Ping

		log.Println("packet received")
		// fmt.Fprintf(os.Stderr, "packet received.\n")

		value, ind, err := decoder.Decode(&com, &stdin, &requestReport, &requestChdir, &ping)
		if err != nil {
			// log.Printf("decode error: %s\n", err)
			fmt.Fprintf(os.Stderr, "decode error: %s\n", err)
			return
		}

		// log.Printf("decode: %d\n", ind)

		switch ind {
		case -1:
			fmt.Fprintln(os.Stderr, "decode error. なぜ?")
			util.Dump(value)

		case 0:
			child := NewChild(com)
			commands[com.UUID] = child
			err := child.Exec()
			if err != nil {
				if err == exec.ErrNotFound {
					errPacket := Error{
						"error",
						com.UUID,
						ErrorCodeNotFound,
						err.Error(),
					}
					encoder.Encode(errPacket)
				} else {
					errPacket := Error{
						"error",
						com.UUID,
						ErrorCodeOther,
						err.Error(),
					}
					encoder.Encode(errPacket)
				}
				fmt.Fprintf(os.Stderr, "fork error: %s\n", err)
				break
			}
			go func(child *Child) {
				for {
					select {
					case buf := <-child.stdout:
						// fmt.Fprintf(os.Stderr, "stdout(%d): %s\n", len(buf), buf)
						stdout := Stdout{"stdout", child.com.UUID, buf}
						err := encoder.Encode(stdout)
						if err != nil {
							fmt.Fprintf(os.Stderr, "%s\n", err)
						}

					case buf := <-child.stderr:
						// fmt.Fprintf(os.Stderr, "stderr(%d): %s\n", len(buf), buf)
						stderr := Stderr{"stderr", child.com.UUID, buf}
						err := encoder.Encode(stderr)
						if err != nil {
							fmt.Fprintf(os.Stderr, "%s\n", err)
						}
					case state := <-child.exitState:
						code := -1

						if status, ok := state.Sys().(syscall.WaitStatus); ok {
							code = status.ExitStatus()
						}

						exit := Exit{
							"exit",
							child.com.UUID,
							state.Success(),
							state.String(),
							uint(state.SystemTime()),
							uint(state.UserTime()),
							code,
						}
						err := encoder.Encode(exit)
						if err != nil {
							fmt.Fprintf(os.Stderr, "%s\n", err)
						}
					}
				}
			}(child)

		case 1:
			cmd, ok := commands[stdin.UUID]
			if ok {
				cmd.stdin <- stdin.Buf
			} else {
				fmt.Fprintln(os.Stderr, "stdin packet: unknown UUID.")
			}

		case 2:
			report(encoder, requestReport.UUID)

		case 3:
			err := os.Chdir(requestChdir.Dir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s\n", err)
			}

			report(encoder, requestChdir.UUID)

		case 4:
			chanPing <- true
		}
		time.Sleep(1 * time.Millisecond)

	}
}
