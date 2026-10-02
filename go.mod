module github.com/PhilemonChiro/volt-cli

go 1.26

require (
	github.com/PhilemonChiro/volt-compiler v0.0.0-00010101000000-000000000000
	github.com/ulikunitz/xz v0.5.17
	go.bug.st/serial v1.8.0
)

require golang.org/x/sys v0.43.0 // indirect

replace github.com/PhilemonChiro/volt-compiler => ../compiler
