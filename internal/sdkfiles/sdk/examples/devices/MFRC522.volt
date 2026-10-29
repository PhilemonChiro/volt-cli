// An RC522 RFID reader (SPI, CS on GPIO 4): log when a card is held near.
hardware {
    device rfid: MFRC522(cs: 4)
}

every 300ms {
    if rfid.is_card_present() == 1 {
        let len: int = rfid.read_uid()
        log("rfid", "card detected, uid length={len}")
    }
}
