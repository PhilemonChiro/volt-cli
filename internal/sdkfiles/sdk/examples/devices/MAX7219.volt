// A MAX7219 8-digit 7-segment display (SPI, CS on GPIO 4) counting up.
hardware {
    device seg: MAX7219(cs: 4)
}

var count: int = 0

setup {
    seg.set_intensity(8)
    seg.clear()
}

every 500ms {
    count += 1
    seg.set_digit(0, count % 10, 0)
    seg.set_digit(1, (count / 10) % 10, 0)
    seg.set_digit(2, (count / 100) % 10, 0)
}
