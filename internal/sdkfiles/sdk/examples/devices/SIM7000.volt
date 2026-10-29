// A SIM7000 LTE modem: keep it healthy, report signal, and answer
// "STATUS" texts from a whitelisted number.
hardware {
    device modem: SIM7000(baud: 115200)
}

setup {
    modem.allow_number("+263770000000")
}

every 30s {
    let net: int = modem.maintain()
    log("modem", "network={net} signal={modem.signal()} op={modem.operator_name()}")
}

every 5s {
    if modem.next_sms() && modem.sms_command_is("STATUS") {
        modem.send_sms(modem.sms_from(), "OK, signal {modem.signal()}")
    }
}
