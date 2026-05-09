package serial

import (
	"strings"
	"sync"
	"time"

	goserial "go.bug.st/serial"
)

type UARTController struct {
	port      goserial.Port
	mu        sync.Mutex
	closeOnce sync.Once
	closeErr  error
}

func NewUARTController(portName string, baudRate int) (*UARTController, error) {
	mode := &goserial.Mode{
		BaudRate: baudRate,
		DataBits: 8,
		Parity:   goserial.NoParity,
		StopBits: goserial.OneStopBit,
	}

	port, err := goserial.Open(portName, mode)
	if err != nil {
		return nil, err
	}

	if err := port.SetDTR(true); err != nil {
		_ = port.Close()
		return nil, err
	}
	time.Sleep(500 * time.Millisecond)

	return &UARTController{port: port}, nil
}

func (u *UARTController) SendRaw(cmd string) error {
	line := strings.TrimSpace(cmd)
	if line == "" {
		return nil
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	_, err := u.port.Write([]byte(line + "\n"))
	return err
}

func (u *UARTController) Read(p []byte) (int, error) {
	return u.port.Read(p)
}

func (u *UARTController) Close() error {
	u.closeOnce.Do(func() {
		u.mu.Lock()
		defer u.mu.Unlock()
		u.closeErr = u.port.Close()
	})
	return u.closeErr
}
