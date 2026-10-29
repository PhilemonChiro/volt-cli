// A DS3231 real-time clock: log the date and time every second.
hardware {
    device rtc: DS3231
}

every 1s {
    log("rtc", "{rtc.year()}-{rtc.month()}-{rtc.date()} {rtc.hours()}:{rtc.minutes()}:{rtc.seconds()}")
}

every 1min {
    let t: float = rtc.temperature()
    log("rtc", "board temperature={t} C")
}
