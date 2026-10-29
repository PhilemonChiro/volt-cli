// A 20x4 I2C LCD: four lines of status, refreshed every second.
hardware {
    device lcd: LCD20x4
}

var ticks: int = 0

setup {
    lcd.backlight(1)
    lcd.clear()
    lcd.set_cursor(0, 0)
    lcd.print("Volt status board")
}

every 1s {
    ticks += 1
    lcd.set_cursor(0, 1)
    lcd.print("Ticks: {ticks}      ")
    lcd.set_cursor(0, 2)
    lcd.print("Minutes: {ticks / 60}   ")
    lcd.set_cursor(0, 3)
    lcd.print("All systems OK")
}
