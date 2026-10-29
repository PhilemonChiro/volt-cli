// QMC5883L: Digital compass / magnetometer — logs its readings every 2 seconds.
hardware {
    device qmc5883l: QMC5883L
}

every 2s {
    let x: float = qmc5883l.x()
    let y: float = qmc5883l.y()
    let z: float = qmc5883l.z()
    let heading: float = qmc5883l.heading()
    log("qmc5883l", "x={x} y={y} z={z} heading={heading}")
}
