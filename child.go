package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/erukiti/go-util"
	"github.com/erukiti/pty"
)

type Child struct {
	com    Command
	stdin  chan []byte
	stdout chan []byte
	stderr chan []byte

	exitState chan *os.ProcessState

	c *exec.Cmd
}

func NewChild(com Command) *Child {
	return &Child{
		com,
		make(chan []byte, 1),
		make(chan []byte, 1),
		make(chan []byte, 1),

		make(chan *os.ProcessState, 1),

		nil,
	}
}

func (c *Child) Exec() error {
	c.c = exec.Command(c.com.Command, c.com.Args...)
	f, e, err := pty.Start2(c.c)
	if err != nil {
		return err
	}

	go func() {
		for {
			state, err := c.c.Process.Wait()
			if err != nil {
				fmt.Fprintf(os.Stderr, "? process wait error: %s\n", err)
				break
			} else if state.Exited() {
				c.exitState <- state
				break
			}
			util.Dump(state)

		}
	}()

	go func() {
		for {
			// fmt.Println("read start.")
			buf := make([]byte, 1024*1024)
			n, err := f.Read(buf)
			if err != nil && err != io.EOF {
				fmt.Fprintf(os.Stderr, "? read error: %s", err)
				return
			}

			if n > 0 {
				c.stdout <- buf[0:n]
			} else {
				time.Sleep(16 * time.Millisecond)
			}
		}
	}()

	go func() {
		for {
			// fmt.Println("read start.")
			buf := make([]byte, 1024*1024)
			n, err := e.Read(buf)
			if err != nil && err != io.EOF {
				fmt.Fprintf(os.Stderr, "? read error: %s", err)
				return
			}

			if n > 0 {
				c.stderr <- buf[0:n]
			} else {
				time.Sleep(16 * time.Millisecond)
			}
		}
	}()

	go func() {
		for {
			select {
			case buf := <-c.stdin:
				f.Write(buf)
			}
		}
	}()

	return nil
}
