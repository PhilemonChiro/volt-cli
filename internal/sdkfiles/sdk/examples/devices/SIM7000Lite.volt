// A SIM7000 modem with the lean driver: keep it healthy, post signal strength
// to a server every 10 minutes, and answer "STATUS" texts from the admin.
// Replace the APN, URL and number with yours.
hardware {
    device modem: SIM7000Lite(baud: 115200)
}

let admin: string = "+263770000000"

setup {
    modem.set_apn("econet.net")
    modem.set_pwrkey(4, true)
}

every 30s {
    let ok: bool = modem.maintain()
    log("modem", "registered={ok} signal={modem.signal()}")
}

every 5s {
    while modem.next_sms() {
        if modem.sms_from() == admin && modem.sms_text() == "STATUS" {
            modem.send_sms(admin, "OK, signal {modem.signal()}")
        }
    }
}

every 10min {
    let status: int = modem.http_post("http://example.com/report", "signal={modem.signal()}")
    log("modem", "report -> {status}")
}
