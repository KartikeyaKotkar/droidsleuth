// Package adb runs bounded, cancellable ADB subprocesses. Linux is the initial host platform.
package adb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

var ErrOutputLimit = errors.New("adb output exceeded 4 MiB")

type Client struct {
	Path    string
	Serial  string
	Timeout time.Duration
}

// boundedBuffer continues draining stderr, but cancels excessive stdout.
type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
	cancel   context.CancelFunc
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.buffer.Len()
	if len(p) > remaining {
		b.overflow = true
		p = p[:remaining]
		if b.cancel != nil {
			b.cancel()
		}
	}
	b.buffer.Write(p)
	return n, nil
}

func (b *boundedBuffer) String() string { return b.buffer.String() }

func (c Client) Run(ctx context.Context, args ...string) (string, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	path := c.Path
	if path == "" {
		path = "adb"
	}
	if c.Serial != "" {
		args = append([]string{"-s", c.Serial}, args...)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	cmd.WaitDelay = 250 * time.Millisecond
	stdout := &boundedBuffer{limit: 4 << 20, cancel: cancel}
	stderr := &boundedBuffer{limit: 8192}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if stdout.overflow {
		return "", ErrOutputLimit
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		// Quoting prevents device text from injecting terminal control sequences.
		return "", fmt.Errorf("adb: %w; stderr=%q", err, stderr.String())
	}
	return stdout.String(), nil
}

func (c Client) Shell(ctx context.Context, script string) (string, error) {
	return c.Run(ctx, "shell", "-T", script)
}

type Device struct {
	Serial string `json:"serial"`
	State  string `json:"state"`
}

func (c Client) Devices(ctx context.Context) ([]Device, error) {
	c.Serial = ""
	out, err := c.Run(ctx, "devices", "-l")
	if err != nil {
		return nil, err
	}
	var devices []Device
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "List of devices attached" {
		return nil, errors.New("unrecognized adb devices response")
	}
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 2 {
			return nil, errors.New("incomplete adb device row")
		}
		devices = append(devices, Device{fields[0], fields[1]})
	}
	return devices, nil
}

func Select(devices []Device, requested string) (string, error) {
	if requested != "" {
		for _, d := range devices {
			if d.Serial == requested {
				if d.State != "device" {
					return "", fmt.Errorf("device %q is %s", requested, d.State)
				}
				return requested, nil
			}
		}
		return "", fmt.Errorf("device %q is not connected", requested)
	}
	if len(devices) != 1 {
		return "", errors.New("select exactly one connected device with --device")
	}
	return Select(devices, devices[0].Serial)
}
