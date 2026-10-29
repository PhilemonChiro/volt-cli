// APDS9960: Gesture, proximity, RGB color sensor — logs its readings every 2 seconds.
hardware {
    device apds9960: APDS9960
}

every 2s {
    let proximity: int = apds9960.proximity()
    let red: int = apds9960.red()
    let green: int = apds9960.green()
    let blue: int = apds9960.blue()
    log("apds9960", "proximity={proximity} red={red} green={green} blue={blue}")
}
