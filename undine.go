package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
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
	Type    string   `msgpack:"type=command"`
	UUID    string   `msgpack:"uuid"`
	Command string   `msgpack:"command"`
	Args    []string `msgpack:"args"`
}

type RequestReport struct {
	Type string `msgpack:"type=request_report"`
	UUID string `msgpack:"uuid"`
}

type RequestChdir struct {
	Type string `msgpack:"type=request_chdir"`
	UUID string `msgpack:"uuid"`
	Dir  string `msgpack:"dir"`
}

type RequestGlob struct {
	Type    string `msgpack:"type=request_glob"`
	UUID    string `msgpack:"uuid"`
	Pattern string `msgpack:"pattern"`
}

type DirEntry struct {
	Type  string   `msgpack:"type=dir_entry"`
	UUID  string   `msgpack:"uuid"`
	Names []string `msgpack:"names"`
}

type Stdin struct {
	Type string `msgpack:"type=stdin"`
	UUID string `msgpack:"uuid"`
	Buf  []byte `msgpack:"buf"`
}

type Stdout struct {
	Type string `msgpack:"type=stdout"`
	UUID string `msgpack:"uuid"`
	Buf  []byte `msgpack:"buf"`
}

type Stderr struct {
	Type string `msgpack:"type=stderr"`
	UUID string `msgpack:"uuid"`
	Buf  []byte `msgpack:"buf"`
}

type Error struct {
	Type    string `msgpack:"type=error"`
	UUID    string `msgpack:"uuid"`
	Code    string `msgpack:"code"`
	Message string `msgpack:"message"`
}

type Exit struct {
	Type     string `msgpack:"type=exit"`
	UUID     string `msgpack:"uuid"`
	Success  bool   `msgpack:"success"`
	Message  string `msgpack:"message"`
	SysTime  uint   `msgpack:"systime"`
	UserTime uint   `msgpack:"usertime"`
	ExitCode int    `msgpack:"code"`
}

type Report struct {
	Type     string            `msgpack:"type=report"`
	UUID     string            `msgpack:"uuid"`
	Username string            `msgpack:"username"`
	Cwd      string            `msgpack:"cwd"`
	Environ  map[string]string `msgpack:"environ"`
}

type ProcessReport struct {
	Pid int `msgpack:"pid"`
}

type Ping struct {
	Type string `msgpack:"type=ping"`
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

	environ := make(map[string]string)
	for _, env := range os.Environ() {
		ar := strings.SplitN(env, "=", 2)
		environ[ar[0]] = ar[1]
	}
	util.Dump(environ)
	report := Report{"report", uuid, usr.Username, cwd, environ}
	err = encoder.Encode(report)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
	}
	log.Print("123")
}

func main() {
	defer func() {
		if err := recover(); err != nil {
			log.Printf("panic: %v", err)
		}
	}()

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
		var requestGlob RequestGlob

		value, ind, err := decoder.Decode(&com, &stdin, &requestReport, &requestChdir, &ping, &requestGlob)
		if err != nil {
			// log.Printf("decode error: %s\n", err)
			fmt.Fprintf(os.Stderr, "decode error: %s\n", err)
			return
		}

		if ind != 4 {
			log.Printf("packet received %d\n", ind)
			util.Dump(value)
		}

		switch ind {
		case -1:
			fmt.Fprintln(os.Stderr, "unknown packet")

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
						// log.Printf("stdout(%d): %s\n", len(buf), buf)
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

		case 5:
			matches, err := filepath.Glob(requestGlob.Pattern)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s\n", err)
			} else {
				dirEntry := DirEntry{"dir_entry", requestGlob.UUID, matches}
				err := encoder.Encode(dirEntry)
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s\n", err)
				}
			}

		}
		time.Sleep(1 * time.Millisecond)

	}
}
