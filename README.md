# volt-cli

The `volt` command of [Volt Embedded](https://voltembedded.com). Volt is a typed language for microcontroller
firmware; this one program creates projects, checks and compiles them, installs the chip toolchains, builds
firmware, flashes the board and shows its serial output. [Volt IDE](https://voltembedded.com) runs the same
program underneath.

## Install

macOS and Linux:

```bash
curl -fsSL https://voltembedded.com/install.sh | sh
```

Windows: download [volt-windows-amd64.zip](https://dl.voltembedded.com/latest/volt-windows-amd64.zip) and put
`volt.exe` on your `PATH`. Every build is listed at [dl.voltembedded.com](https://dl.voltembedded.com/latest/SHA256SUMS)
with its checksum. `volt update` installs the latest release.

## Quick start

```bash
volt new blink --chip esp32
cd blink
volt check        # type-check and validate the hardware, no build
volt run          # build, flash the connected board, then monitor it
```

The first build for a chip installs its toolchain into `~/.volt/toolchains`; downloads are signature-checked.

## Commands

| Command | What it does |
|---------|--------------|
| `volt new <name>` | Create a project: `volt.toml` and a starting `main.volt` |
| `volt check` | Type-check and validate the hardware without building |
| `volt compile` | Compile to C |
| `volt fmt` | Format `.volt` files |
| `volt build` | Build firmware into `<project>/build/<chip>/` |
| `volt flash` | Flash the last build to a connected board |
| `volt run` | Build, flash, then monitor |
| `volt monitor` | Show a board's serial output |
| `volt ports` | List connected boards |
| `volt toolchain` | Check, install or repair a chip's toolchain |
| `volt sdk sync` | Update the device library |
| `volt trust` | Show or record which device libraries you approved |
| `volt history` | The project's automatic change history, with diffs and undo |
| `volt spec` | Print the language reference |
| `volt mcp` | Serve Volt's tools (check, device cards, the language reference) to coding agents over MCP |
| `volt update` | Update `volt` to the latest release |

`volt help` lists every command with its options.

## Develop

Requires Go 1.26. The CLI builds against [volt-compiler](https://github.com/PhilemonChiro/volt-compiler), which
`go.mod` expects in a sibling folder named `compiler`:

```bash
git clone https://github.com/PhilemonChiro/volt-compiler compiler
git clone https://github.com/PhilemonChiro/volt-cli cli
cd cli
go build ./cmd/volt
go test ./...
```

On macOS the build needs cgo (serial port listing uses IOKit). Releases are built with `scripts/release.sh`.

| Path | Contents |
|------|----------|
| `cmd/volt/` | The commands |
| `internal/build/` | Firmware builds (ESP-IDF, Pico SDK), size reports |
| `internal/toolchain/` | Toolchain download, verification and install |
| `internal/esptool/`, `internal/device/` | Flashing and serial ports |
| `internal/libtrust/` | Device library approval records |
| `internal/sdk/`, `internal/sdkfiles/` | The bundled runtime and device library |

## License

[GNU AGPL v3](LICENSE). A commercial licence is available: contact licensing@voltembedded.com.
Contributions are made under the [Contributor License Agreement](CLA.md); see [CONTRIBUTING.md](CONTRIBUTING.md).
