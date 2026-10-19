package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/danielpaulus/go-ios/ios"
	"github.com/sirupsen/logrus"
)

var version = "dev"

const usage = `usage: ios-screencast <command> [options]

commands:
  start      Start streaming and serve the viewer
  devices    List connected devices
  version    Print the version

options:
  --udid <udid>          Device to use (default: first connected)
  --mode <mode>          mix (default)  ReplayKit, switching to the runner while a system alert is visible
                         runner         runner screenshots only (sees everything, lower fps)
                         broadcast      ReplayKit only (smoothest, misses some system alerts)
  --fps <n>              Frames per second (default 60)
  --scale <0.1-1.0>      Output scale of the native resolution (default 1.0)
  --bitrate <mbps>       Bitrate in Mbps (default 12)
  --stay                 Stay in the app after starting the broadcast (default: go to the home screen)
  --broadcast-attempts <n>  How often to try starting the broadcast automatically (default 1, 0 = never)
  --port <port>          Viewer port (default 1881)
  --stdout               Also write the raw H.264 stream to stdout

Before the first start, sign and install ios-screencast.ipa and ios-screencast-runner.ipa
from https://github.com/hakanor/ios-screencast/releases with your own signing tool.

examples:
  ios-screencast start
  ios-screencast start --mode runner --fps 15 --scale 0.5
  ios-screencast start --stdout | ffplay -f h264 -fflags nobuffer -flags low_delay -
`

type options struct {
	command           string
	udid              string
	settings          Settings
	stay              bool
	broadcastAttempts int
	port              int
	stdout            bool
}

func parseOptions(args []string) options {
	o := options{command: "help", settings: defaultSettings, port: 1881, broadcastAttempts: 1}
	if len(args) > 0 {
		o.command = args[0]
		args = args[1:]
	}

	value := func(i *int, flag string) string {
		*i++
		if *i >= len(args) {
			fail("missing value for %s", flag)
		}
		return args[*i]
	}
	number := func(i *int, flag string) float64 {
		n, err := strconv.ParseFloat(value(i, flag), 64)
		if err != nil {
			fail("%s needs a number", flag)
		}
		return n
	}

	for i := 0; i < len(args); i++ {
		switch flag := args[i]; flag {
		case "--udid":
			o.udid = value(&i, flag)
		case "--mode":
			switch mode := Mode(value(&i, flag)); mode {
			case ModeMix, ModeRunner, ModeBroadcast:
				o.settings.Mode = mode
			default:
				fail("--mode must be mix, runner or broadcast")
			}
		case "--fps":
			o.settings.FPS = int(number(&i, flag))
		case "--scale":
			o.settings.Scale = number(&i, flag)
		case "--bitrate":
			o.settings.Bitrate = int(number(&i, flag) * 1_000_000)
		case "--stay":
			o.stay = true
		case "--broadcast-attempts":
			o.broadcastAttempts = int(number(&i, flag))
		case "--port":
			o.port = int(number(&i, flag))
		case "--stdout":
			o.stdout = true
		case "-h", "--help":
			o.command = "help"
		default:
			fail("unknown option %s", flag)
		}
	}
	return o
}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[ios-screencast] "+format+"\n", args...)
}

func fail(format string, args ...any) {
	logf(format, args...)
	os.Exit(1)
}

func quietGoIOS() {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	logrus.SetOutput(io.Discard)
	log.SetOutput(io.Discard)
}

func main() {
	o := parseOptions(os.Args[1:])
	if os.Getenv("SCREENCAST_DEBUG") == "" {
		quietGoIOS()
	}

	switch o.command {
	case "version", "--version", "-v":
		fmt.Println(version)

	case "devices":
		list, err := ios.ListDevices()
		if err != nil {
			fail("cannot list devices: %v", err)
		}
		for _, device := range list.DeviceList {
			fmt.Println(device.Properties.SerialNumber)
		}

	case "start":
		device, err := findDevice(o.udid)
		if err != nil {
			fail("%v", err)
		}
		apps, err := findApps(device)
		if err != nil {
			fail("%v", err)
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := newSession(o, device, apps).run(ctx); err != nil {
			fail("%v", err)
		}
		logf("stopped")

	default:
		fmt.Print(usage)
	}
}
