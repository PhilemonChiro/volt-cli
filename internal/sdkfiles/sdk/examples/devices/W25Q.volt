// W25Q SPI flash (CS on GPIO 4). This library doesn't give Volt programs any
// methods yet — declaring it reserves its SPI pins; use the storage built-ins
// (save / load) to keep data across restarts.
hardware {
    device flash: W25Q(cs: 4)
}

setup {
    log("flash", "W25Q declared on CS GPIO 4")
}
