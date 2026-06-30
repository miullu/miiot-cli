package main

import (
	"fmt"
	"os"
	"strings"
)

func usage() {
	fmt.Fprintf(os.Stderr, `miiot-cli: Control Xiaomi MIoT/miIO devices locally.

Usage:
  miiot-cli list                          - List stored devices
  miiot-cli add <name> <host> <token>     - Add a device
  miiot-cli remove <name>                 - Remove a device
  miiot-cli <name> info                   - Get device info
  miiot-cli <name> on                     - Turn light on
  miiot-cli <name> off                    - Turn light off
  miiot-cli <name> status                 - Get device status (all properties)
  miiot-cli <name> detect                 - Detect MIoT vs miIO protocol
  miiot-cli <name> discover               - Probe all known properties
  miiot-cli <name> spec                   - Fetch and cache MIoT spec
  miiot-cli <name> get <prop>             - Read a property (e.g. power,brightness)
  miiot-cli <name> set <prop> <value>     - Set a property
  miiot-cli <name> brightness <val>       - Set brightness (0-100)
  miiot-cli <name> mode <val>             - Set mode (e.g. 0=day,1=night)
  miiot-cli <name> colortemp <val>        - Set color temperature (kelvin)
  miiot-cli <name> props                  - List all properties with values
  miiot-cli <name> miot-get <siid.piid>...   - Read MIoT properties (batched)
  miiot-cli <name> miot-set <siid.piid> <v>  - Set MIoT property
  miiot-cli <name> miio-get <prop>...        - Read miIO properties
  miiot-cli <name> miio-set <prop> <value>   - Set miIO property
  miiot-cli <name> send <method> [args...]   - Send raw command
  miiot-cli serve [--listen <addr+port>]  - Start web dashboard (default :8080)

Legacy (direct IP/token, no CSV):
  miiot-cli <host> <token> <command> [args...]

Environment:
  MIIOT_CLI_PATH    Data directory (devices.csv, spec cache, etc.)
                    Default: ~/.cache/miiot-cli
  MIIOT_CLI_WEB     Path to custom HTML dashboard file (served instead of built-in)
`)
}

func cmdList() int {
	devices, err := LoadDevices()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error loading devices:", err)
		return 1
	}
	if len(devices) == 0 {
		fmt.Println("No devices stored.")
		fmt.Println("Use: miiot-cli add <name> <host> <token>")
		return 0
	}
	fmt.Printf("%-20s %-21s %-32s\n", "Name", "IP", "Token")
	fmt.Println(strings.Repeat("-", 75))
	for _, d := range devices {
		tok := d.Token
		if len(tok) > 8 {
			tok = tok[:4] + "..." + tok[len(tok)-4:]
		}
		fmt.Printf("%-20s %-21s %-32s\n", d.Name, d.Host, tok)
	}
	return 0
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	force := false
	var cleanArgs []string
	for _, arg := range os.Args[1:] {
		if arg == "--force" {
			force = true
		} else {
			cleanArgs = append(cleanArgs, arg)
		}
	}

	first := cleanArgs[0]

	switch first {
	case "help", "--help", "-h":
		usage()
		return

	case "list":
		os.Exit(cmdList())

	case "add":
		if len(cleanArgs) < 4 {
			fmt.Fprintln(os.Stderr, "Usage: miiot-cli add <name> <host> <token>")
			os.Exit(1)
		}
		if err := AddDevice(cleanArgs[1], cleanArgs[2], cleanArgs[3]); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		fmt.Printf("Device '%s' added.\n", cleanArgs[1])

	case "remove":
		if len(cleanArgs) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: miiot-cli remove <name>")
			os.Exit(1)
		}
		if err := RemoveDevice(cleanArgs[1]); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		fmt.Printf("Device '%s' removed.\n", cleanArgs[1])

	case "serve":
		port := ":8080"
		for i, arg := range cleanArgs {
			if arg == "--listen" && i+1 < len(cleanArgs) {
				port = ":" + cleanArgs[i+1]
			}
		}
		fmt.Printf("Starting web server on %s\n", port)
		if err := serveWeb(port); err != nil {
			fmt.Fprintln(os.Stderr, "Server error:", err)
			os.Exit(1)
		}

	default:
		if entry, err := FindDevice(first); err == nil {
			os.Exit(runWithDevice(entry.Host, entry.Token, cleanArgs[1:], force))
			return
		}

		if len(cleanArgs) >= 2 {
			os.Exit(runWithDevice(cleanArgs[0], cleanArgs[1], cleanArgs[2:], force))
			return
		}

		fmt.Fprintf(os.Stderr, "Unknown command or device: %s\n", first)
		usage()
		os.Exit(1)
	}
}
