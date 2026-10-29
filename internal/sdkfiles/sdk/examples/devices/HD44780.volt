// A 16x2 (or 20x4) I2C character LCD: a title on the first line, a live
// counter on the second. For a 20x4 use HD44780(cols: 20, rows: 4).
hardware {
    device lcd: HD44780(cols: 16, rows: 2)
}

var count: int = 0

setup {
    lcd.backlight(1)
    lcd.clear()
    lcd.set_cursor(0, 0)
    lcd.print("Hello, Volt!")
}

every 1s {
    count += 1
    lcd.set_cursor(0, 1)
    lcd.print("Count: {count}   ")
}
