// An nRF24L01 2.4 GHz radio: send a numbered message every second.
hardware {
    device radio: NRF24L01(cs: 5, ce: 4, channel: 76)
}

var n: int = 0

every 1s {
    n += 1
    let ok: int = radio.send("ping {n}")
    log("radio", "sent ping {n}, ok={ok}")
}
