package rgb11wallet

import strict "github.com/sat20-labs/rgb11/strict_encoding"

// Channel pending records have their own record kind. Existing pending records
// keep their original byte representation and remain readable unchanged.
func encodeChannelSend(e *strict.Encoder, value *ChannelSendData) error {
	for _, text := range []string{value.ChannelID, value.Reason} {
		if err := encodeText(e, text); err != nil {
			return err
		}
	}
	for _, blob := range [][]byte{value.WitnessScript, value.PeerPubKey, value.MoreData} {
		if err := encodeBlob(e, blob); err != nil {
			return err
		}
	}
	var flags uint8
	if value.PayFeeByLocal {
		flags |= 1
	}
	if value.Signed {
		flags |= 2
	}
	return e.U8(flags)
}

func decodeChannelSend(d *strict.Decoder, value *ChannelSendData) error {
	var err error
	if value.ChannelID, err = decodeText(d); err != nil {
		return err
	}
	if value.Reason, err = decodeText(d); err != nil {
		return err
	}
	for _, target := range []*[]byte{&value.WitnessScript, &value.PeerPubKey, &value.MoreData} {
		if *target, err = decodeBlob(d); err != nil {
			return err
		}
	}
	flags, err := d.U8()
	if err != nil {
		return err
	}
	if flags & ^uint8(3) != 0 {
		return ErrRGB11Inconsistent
	}
	value.PayFeeByLocal, value.Signed = flags&1 != 0, flags&2 != 0
	return nil
}
