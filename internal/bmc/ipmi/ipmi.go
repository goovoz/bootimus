package ipmi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"bootimus/internal/bmc"

	"github.com/creack/pty"
)

const DefaultPort = 623

var ErrNotInstalled = errors.New("ipmitool not found on PATH; install ipmitool to use the IPMI serial console")

var lookPath = exec.LookPath

type Console struct {
	cmd  *exec.Cmd
	tty  *os.File
	args []string
	env  []string
	once sync.Once
}

func baseArgs(host string, port int, username string) []string {
	if port == 0 {
		port = DefaultPort
	}
	return []string{"-I", "lanplus", "-H", host, "-p", strconv.Itoa(port), "-U", username, "-E"}
}

func Open(ctx context.Context, host string, port int, username, password string) (bmc.Console, error) {
	path, err := lookPath("ipmitool")
	if err != nil {
		return nil, ErrNotInstalled
	}
	args := baseArgs(host, port, username)
	env := append(os.Environ(), "IPMI_PASSWORD="+password)
	cmd := exec.Command(path, append(args, "sol", "activate")...)
	cmd.Env = env
	tty, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("start ipmitool: %w", err)
	}
	return &Console{cmd: cmd, tty: tty, args: args, env: env}, nil
}

func (c *Console) Read(p []byte) (int, error) {
	return c.tty.Read(p)
}

func (c *Console) Write(p []byte) (int, error) {
	return c.tty.Write(p)
}

func (c *Console) Close() error {
	c.once.Do(func() {
		c.tty.Write([]byte("\r~."))
		done := make(chan struct{})
		go func() {
			c.cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			c.cmd.Process.Kill()
			<-done
		}
		c.tty.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		deactivate := exec.CommandContext(ctx, c.cmd.Path, append(c.args, "sol", "deactivate")...)
		deactivate.Env = c.env
		deactivate.Run()
	})
	return nil
}
