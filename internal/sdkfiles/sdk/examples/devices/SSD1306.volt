// A 128x64 I2C OLED: draw text, then show() to put it on screen.
hardware {
    device oled: SSD1306
}

var n: int = 0

every 1s {
    n += 1
    oled.clear()
    oled.text(0, 0, "Hello, Volt!")
    oled.text(0, 16, "Count: {n}")
    oled.show()
}
