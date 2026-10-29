// HC05: Bluetooth serial module on UART — echo every line a phone sends,
// and say hello every 10 seconds (use a Bluetooth serial terminal app).
hardware {
    device bt: HC05(baud: 9600)
}

every 100ms {
    let line: string = bt.read_line()
    if line != "" {
        bt.send_line("You said: {line}")
    }
}

every 10s {
    bt.send_line("Hello from Volt")
}
