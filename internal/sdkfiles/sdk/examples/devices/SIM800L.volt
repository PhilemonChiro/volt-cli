// A SIM800L GSM modem on UART: wait for the network, then send one SMS.
// Replace the number with yours.
hardware {
    device gsm: SIM800L(baud: 9600)
}

var sent: bool = false

every 5s {
    let signal: int = gsm.signal()
    log("gsm", "signal={signal} registered={gsm.registered()}")
    if !sent && gsm.registered() {
        sent = gsm.send_sms("+263770000000", "Hello from Volt")
        log("gsm", "sms sent={sent}")
    }
}
