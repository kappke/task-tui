package bootstrap

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"syscall"
)

// Key is a normalized terminal input event.
type Key struct {
	Rune rune
	Name string
}

const (
	KeyUp        = "up"
	KeyDown      = "down"
	KeyLeft      = "left"
	KeyRight     = "right"
	KeyEnter     = "enter"
	KeyEscape    = "escape"
	KeyBackspace = "backspace"
)

// Terminal is injectable so lifecycle tests do not need a real TTY.
type Terminal interface {
	Initialize(context.Context) error
	Restore(context.Context) error
	Write(context.Context, []byte) error
	ReadKey(context.Context) (Key, error)
	Headless() bool
}

// TerminalStreams exposes the underlying streams to terminal frameworks that
// own raw-mode input and screen restoration, such as Bubble Tea. It is kept as
// a separate optional interface so lifecycle fakes only need to implement the
// original Terminal contract.
type TerminalStreams interface {
	Input() io.Reader
	Output() io.Writer
}

// StreamTerminal provides a small ANSI terminal implementation. It uses an
// alternate screen but does not own process-global terminal state; Restore is
// explicit and idempotent.
type StreamTerminal struct {
	input     io.Reader
	output    io.Writer
	inputFile *os.File
	headless  bool

	mu          sync.Mutex
	restored    bool
	initialized bool
}

// NewStreamTerminal creates a terminal over input and output. When headless is
// true no ANSI control sequences are emitted and Run returns after the cached
// initial render.
func NewStreamTerminal(input io.Reader, output io.Writer, headless bool) *StreamTerminal {
	if input == nil {
		input = os.Stdin
	}
	if output == nil {
		output = os.Stdout
	}
	file, _ := input.(*os.File)
	return &StreamTerminal{input: input, output: output, inputFile: file, headless: headless}
}

// NewDefaultTerminal selects headless mode automatically when streams are not
// character devices.
func NewDefaultTerminal(input io.Reader, output io.Writer) *StreamTerminal {
	if input == nil {
		input = os.Stdin
	}
	if output == nil {
		output = os.Stdout
	}
	headless := !isCharDevice(input) || !isCharDevice(output)
	return NewStreamTerminal(input, output, headless)
}

// Input returns the stream used for terminal input.
func (t *StreamTerminal) Input() io.Reader {
	if t == nil {
		return nil
	}
	return t.input
}

// Output returns the stream used for terminal output.
func (t *StreamTerminal) Output() io.Writer {
	if t == nil {
		return nil
	}
	return t.output
}

func (t *StreamTerminal) Headless() bool { return t == nil || t.headless }

func (t *StreamTerminal) Initialize(ctx context.Context) error {
	if t == nil {
		return errors.New("initialize terminal: nil terminal")
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.initialized {
		return nil
	}
	t.initialized = true
	t.restored = false
	if t.headless {
		return nil
	}
	if _, err := io.WriteString(t.output, "\x1b[?1049h\x1b[?25l\x1b[H\x1b[2J"); err != nil {
		t.initialized = false
		return err
	}
	return nil
}

func (t *StreamTerminal) Restore(ctx context.Context) error {
	if t == nil {
		return nil
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.restored || !t.initialized {
		return nil
	}
	t.restored = true
	if t.headless {
		return nil
	}
	if _, err := io.WriteString(t.output, "\x1b[?25h\x1b[?1049l"); err != nil {
		return err
	}
	return nil
}

func (t *StreamTerminal) Write(ctx context.Context, data []byte) error {
	if t == nil {
		return errors.New("write terminal: nil terminal")
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	_, err := t.output.Write(data)
	return err
}

func (t *StreamTerminal) ReadKey(ctx context.Context) (Key, error) {
	if t == nil {
		return Key{}, errors.New("read terminal: nil terminal")
	}
	if t.headless {
		return Key{}, io.EOF
	}
	if err := contextError(ctx); err != nil {
		return Key{}, err
	}
	first, err := t.readByte(ctx)
	if err != nil {
		return Key{}, err
	}
	switch first {
	case 3:
		return Key{Name: "quit"}, nil
	case '\r', '\n':
		return Key{Name: KeyEnter}, nil
	case 127, 8:
		return Key{Name: KeyBackspace}, nil
	case 27:
		second, err := t.readByte(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) {
				return Key{Name: KeyEscape}, nil
			}
			return Key{}, err
		}
		if second != '[' {
			return Key{Name: KeyEscape}, nil
		}
		third, err := t.readByte(ctx)
		if err != nil {
			return Key{}, err
		}
		switch third {
		case 'A':
			return Key{Name: KeyUp}, nil
		case 'B':
			return Key{Name: KeyDown}, nil
		case 'C':
			return Key{Name: KeyRight}, nil
		case 'D':
			return Key{Name: KeyLeft}, nil
		default:
			return Key{Name: KeyEscape}, nil
		}
	default:
		return Key{Rune: rune(first)}, nil
	}
}

func (t *StreamTerminal) readByte(ctx context.Context) (byte, error) {
	if t.inputFile != nil {
		if err := waitReadable(ctx, t.inputFile); err != nil {
			return 0, err
		}
		var data [1]byte
		n, err := t.inputFile.Read(data[:])
		if err != nil {
			return 0, err
		}
		if n != 1 {
			return 0, io.ErrUnexpectedEOF
		}
		return data[0], nil
	}
	var data [1]byte
	n, err := io.ReadFull(t.input, data[:])
	if err != nil {
		return 0, err
	}
	if n != 1 {
		return 0, io.ErrUnexpectedEOF
	}
	return data[0], nil
}

func waitReadable(ctx context.Context, file *os.File) error {
	fd := int(file.Fd())
	for {
		if err := contextError(ctx); err != nil {
			return err
		}
		var set syscall.FdSet
		set.Bits[fd/64] |= 1 << uint(fd%64)
		timeout := syscall.Timeval{Sec: 0, Usec: 100000}
		n, err := syscall.Select(fd+1, &set, nil, nil, &timeout)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			return err
		}
		if n > 0 {
			return nil
		}
	}
}

func isCharDevice(value interface{}) bool {
	file, ok := value.(*os.File)
	if !ok || file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("nil context")
	}
	return ctx.Err()
}
