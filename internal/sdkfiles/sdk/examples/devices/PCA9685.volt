// A PCA9685 16-channel servo/PWM board: sweep the servo on channel 0
// between 0 and 180 degrees.
hardware {
    device servos: PCA9685
}

var angle: int = 0
var step: int = 10

every 100ms {
    servos.set_servo(0, angle)
    angle += step
    if angle >= 180 || angle <= 0 {
        step = 0 - step
    }
}
