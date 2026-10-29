// HMC5883L: 3-axis magnetometer / compass (found on the GY-85 9-DoF board) — logs its readings every 2 seconds.
hardware {
    device hmc5883l: HMC5883L
}

every 2s {
    let x: float = hmc5883l.x()
    let y: float = hmc5883l.y()
    let z: float = hmc5883l.z()
    let heading: float = hmc5883l.heading()
    log("hmc5883l", "x={x} y={y} z={z} heading={heading}")
}
