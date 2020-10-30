// Copyright 2019 Northern.tech AS
//
//    Licensed under the Apache License, Version 2.0 (the "License");
//    you may not use this file except in compliance with the License.
//    You may obtain a copy of the License at
//
//        http://www.apache.org/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS,
//    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//    See the License for the specific language governing permissions and
//    limitations under the License.
package progressbar

// TODO -- Add terminal width respect

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
	// "golang.org/x/sys/unix" - Do not add for now (split into minimal and not (?))
)

// Client needs (TTY & NON-TTY):
//
// - Tick
// - Write
//
// Mender-Artifact needs (TTY-only):
// - Tick
// - Finish
// - Reset
// - ...

type Renderer interface {
	Render(int) // Write the progressbar
}

// type MinimalBar struct {
// 	renderer Renderer
// }

// Only implements Tick()
// func NewMinimal() *MinimalBar {
// 	return &MinimalBar{}
// }

// func (m *MinimalBar) Tick(n int) {
// 	return
// }

type Bar struct {
	Size         int64 // size of the input
	currentCount int64 // current count
	Renderer
}

func New() *Bar {
	// terminalSize()
	if !isatty.IsTerminal(os.Stderr.Fd()) {
		return &Bar{
			Renderer: &TTYRenderer{
				Out:            os.Stderr,
				ProgressMarker: ".",
				terminalWidth:  140, // For now
			},
		}
	} else {
		return &Bar{
			Renderer: &NoTTYRenderer{
				Out:            os.Stderr,
				ProgressMarker: ".",
				terminalWidth:  80,
			},
		}
	}
}

func (b *Bar) Tick(n int64) {
	b.currentCount += n
	if b.Size > 0 {
		b.Renderer.Render(int(float64(b.currentCount) / float64(b.Size) * 100))
	}
}

func (b *Bar) Reset() {
	b.currentCount = 0
	b.Renderer.Render(0)
}

func (b *Bar) Finish() {
	b.Renderer.Render(100)
}

// func terminalSize() {
// 	ws, _ := unix.IoctlGetWinsize(1, unix.TIOCGWINSZ)
// 	fmt.Printf("terminalSize: %d\n", ws)

// }

type TTYRenderer struct {
	Out            io.Writer // output device
	ProgressMarker string
	terminalWidth  int // Width of the terminal (assume 80 for now - ie. hardcoded)
}

func (p *TTYRenderer) Render(percentage int) {
	suffix := fmt.Sprintf(" - %3d %%", percentage)
	widthAvailable := p.terminalWidth - len(suffix)
	number_of_dots := int((float64(widthAvailable) * float64(percentage)) / 100)
	number_of_fillers := widthAvailable - number_of_dots
	if percentage > 100 {
		fmt.Println("Percentage is > 100")
		number_of_dots = widthAvailable
		number_of_fillers = 0
	}
	if percentage < 0 {
		fmt.Println("Negative percentage...")
		return
	}
	if number_of_dots < 0 {
		fmt.Println("Negative # o dots...")
		return
	}
	if number_of_fillers < 0 {
		fmt.Println("Negative # o fillers...")
		return
	}
	fmt.Fprintf(p.Out, "\r%s%s%s",
		strings.Repeat(p.ProgressMarker, number_of_dots),
		strings.Repeat(" ", number_of_fillers),
		suffix)
}

type NoTTYRenderer struct {
	Out            io.Writer // output device
	ProgressMarker string
	lastPercent    int
	terminalWidth  int // How wide is the progressbar we should render (?)
}

func (p *NoTTYRenderer) Render(percentage int) {
	if percentage > p.lastPercent {
		number_of_dots := int((float64(p.terminalWidth) * float64(percentage-p.lastPercent)) / 100)
		str := strings.Repeat(p.ProgressMarker, number_of_dots)
		if number_of_dots > 0 {
			p.lastPercent = percentage
		}
		fmt.Fprintf(p.Out, str)
	}
}
