//go:build linux

package desktop

import (
	"strconv"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// On Linux a prompt is a desktop notification with one action per option.
// KDE Plasma shows the actions as buttons; the notification takes no
// focus and goes away by itself.

const (
	notificationsService   = "org.freedesktop.Notifications"
	notificationsPath      = "/org/freedesktop/Notifications"
	notificationsInterface = "org.freedesktop.Notifications"
)

var (
	sessionMu   sync.Mutex
	sessionConn *dbus.Conn

	promptMu      sync.Mutex
	promptShowing bool
	// promptWaiting maps the notification shown to its answer.
	promptWaiting = map[uint32]chan int{}
)

// sessionBus is this process's own session bus connection for notices and
// the grayscale filter, made again when the session's bus went away. It
// hears the notifications' answers.
func sessionBus() (*dbus.Conn, error) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	if sessionConn != nil && sessionConn.Connected() {
		return sessionConn, nil
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, err
	}
	if err := conn.AddMatchSignal(dbus.WithMatchInterface(notificationsInterface), dbus.WithMatchObjectPath(notificationsPath)); err != nil {
		conn.Close()
		return nil, err
	}
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	go hearNotifications(signals)
	sessionConn = conn
	return conn, nil
}

// hearNotifications passes a notification's button, or its closing
// unanswered, to the prompt waiting for it.
func hearNotifications(signals <-chan *dbus.Signal) {
	for signal := range signals {
		if len(signal.Body) < 2 {
			continue
		}
		id, ok := signal.Body[0].(uint32)
		if !ok {
			continue
		}
		answer := -1
		switch signal.Name {
		case notificationsInterface + ".ActionInvoked":
			key, _ := signal.Body[1].(string)
			index, err := strconv.Atoi(key)
			if err != nil {
				// "default" is a click on the notification itself.
				continue
			}
			answer = index
		case notificationsInterface + ".NotificationClosed":
		default:
			continue
		}
		promptMu.Lock()
		waiting := promptWaiting[id]
		delete(promptWaiting, id)
		promptMu.Unlock()
		if waiting != nil {
			waiting <- answer
		}
	}
}

// Prompt shows a notification with a title, an optional detail line and
// one button per option, and returns the index of the button pressed, or -1
// when it went away unanswered, no notification service runs, or another
// prompt is already showing.
func Prompt(title, detail string, options []string, timeout time.Duration) int {
	promptMu.Lock()
	if promptShowing {
		promptMu.Unlock()
		return -1
	}
	promptShowing = true
	promptMu.Unlock()
	defer func() {
		promptMu.Lock()
		promptShowing = false
		promptMu.Unlock()
	}()
	conn, err := sessionBus()
	if err != nil {
		return -1
	}
	actions := []string{}
	for index, option := range options {
		actions = append(actions, strconv.Itoa(index), option)
	}
	var id uint32
	err = conn.Object(notificationsService, notificationsPath).Call(notificationsInterface+".Notify", 0,
		"Telemetry", uint32(0), "", title, detail, actions, map[string]dbus.Variant{}, int32(timeout.Milliseconds())).Store(&id)
	if err != nil {
		return -1
	}
	// A button needs a person to press it, so the answer comes well after
	// the notification's ID.
	answer := make(chan int, 1)
	promptMu.Lock()
	promptWaiting[id] = answer
	promptMu.Unlock()
	select {
	case index := <-answer:
		return index
	case <-time.After(timeout + 5*time.Second):
		promptMu.Lock()
		delete(promptWaiting, id)
		promptMu.Unlock()
		conn.Object(notificationsService, notificationsPath).Call(notificationsInterface+".CloseNotification", 0, id)
		return -1
	}
}

// PromptSupported reports whether Prompt shows anything here: whether a
// notification service runs.
func PromptSupported() bool {
	conn, err := sessionBus()
	if err != nil {
		return false
	}
	var running bool
	if err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, notificationsService).Store(&running); err != nil {
		return false
	}
	return running
}

// PromptShowing reports whether a prompt is showing.
func PromptShowing() bool {
	promptMu.Lock()
	defer promptMu.Unlock()
	return promptShowing
}
