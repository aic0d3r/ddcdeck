package appwatch

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// watchWM follows the focused window on Hyprland or Sway and reports its class to
// onClass until ctx ends. It returns an error when neither compositor is running.
func watchWM(ctx context.Context, onClass func(string)) (string, error) {
	if sig := os.Getenv("HYPRLAND_INSTANCE_SIGNATURE"); sig != "" {
		sock := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "hypr", sig, ".socket2.sock")
		c, err := net.Dial("unix", sock)
		if err != nil {
			return "", err
		}
		go func() { <-ctx.Done(); c.Close() }()
		go readLines(c, func(l string) {
			if class, ok := hyprClass(l); ok {
				onClass(class)
			}
		})
		return "Hyprland", nil
	}
	if os.Getenv("SWAYSOCK") != "" {
		cmd := exec.CommandContext(ctx, "swaymsg", "-t", "subscribe", "-m", `["window"]`)
		out, err := cmd.StdoutPipe()
		if err != nil {
			return "", err
		}
		if err := cmd.Start(); err != nil {
			return "", err
		}
		go func() {
			readLines(out, func(l string) {
				if class, ok := swayClass(l); ok {
					onClass(class)
				}
			})
			_ = cmd.Wait()
		}()
		return "Sway", nil
	}
	return "", fmt.Errorf("no Hyprland or Sway session")
}

func readLines(r interface{ Read([]byte) (int, error) }, f func(string)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		f(sc.Text())
	}
}

// hyprClass parses "activewindow>>class,title". The title may contain commas, the class
// cannot.
func hyprClass(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, "activewindow>>")
	if !ok {
		return "", false
	}
	class, _, _ := strings.Cut(rest, ",")
	return class, class != ""
}

// swayClass parses a window event; native Wayland windows have app_id, XWayland ones a
// window_properties.class.
func swayClass(line string) (string, bool) {
	var ev struct {
		Change    string `json:"change"`
		Container struct {
			AppID string `json:"app_id"`
			Props struct {
				Class string `json:"class"`
			} `json:"window_properties"`
		} `json:"container"`
	}
	if json.Unmarshal([]byte(line), &ev) != nil || ev.Change != "focus" {
		return "", false
	}
	if ev.Container.AppID != "" {
		return ev.Container.AppID, true
	}
	return ev.Container.Props.Class, ev.Container.Props.Class != ""
}
