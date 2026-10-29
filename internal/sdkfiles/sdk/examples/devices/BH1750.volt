// BH1750: Ambient light sensor — logs its readings every 2 seconds.
hardware {
    device bh1750: BH1750
}

every 2s {
    let lux: float = bh1750.lux()
    log("bh1750", "lux={lux}")
}
