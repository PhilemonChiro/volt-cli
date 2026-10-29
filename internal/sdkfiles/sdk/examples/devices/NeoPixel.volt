// A strip of 8 WS2812B LEDs on GPIO 4: walk one red pixel along the strip.
// set() and fill() change the colours; show() sends them to the LEDs.
hardware {
    device strip: NeoPixel(pin: 4, leds: 8)
}

var pos: int = 0

setup {
    strip.set_brightness(40)
    strip.clear()
    strip.show()
}

every 150ms {
    strip.fill(0, 0, 10)
    strip.set(pos, 255, 0, 0)
    strip.show()
    pos = (pos + 1) % 8
}
