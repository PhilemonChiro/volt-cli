// MLX90614: Non-contact IR thermometer — logs its readings every 2 seconds.
hardware {
    device mlx90614: MLX90614
}

every 2s {
    let ambient: float = mlx90614.ambient()
    let object: float = mlx90614.object()
    log("mlx90614", "ambient={ambient} object={object}")
}
