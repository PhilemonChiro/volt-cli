// A 16x2 I2C LCD showing uptime in seconds.
hardware {
    device lcd: LCD16x2
}

var secs: int = 0

setup {
    lcd.backlight(1)
    lcd.clear()
    lcd.print("Uptime")
}

every 1s {
    secs += 1
    lcd.set_cursor(0, 1)
    lcd.print("{secs} s      ")
}
