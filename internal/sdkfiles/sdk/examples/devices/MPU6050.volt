// MPU6050: 6-axis accelerometer and gyroscope — logs its readings every 2 seconds.
hardware {
    device mpu6050: MPU6050
}

every 2s {
    let accel_x: float = mpu6050.accel_x()
    let accel_y: float = mpu6050.accel_y()
    let accel_z: float = mpu6050.accel_z()
    let gyro_x: float = mpu6050.gyro_x()
    log("mpu6050", "accel_x={accel_x} accel_y={accel_y} accel_z={accel_z} gyro_x={gyro_x}")
}
