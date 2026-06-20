package wallet

func VerifyPaymentTx(payment *PaymentReservation) error {
	err := VerifyTxWithChannel_SatsNet(payment.Channel, payment.PaymentTx, payment.PaymentPreFetcher, payment.RemotePaymentSig)
	if err != nil {
		Log.Errorf("VerifyTx_SatsNet failed. %v", err)
		return err
	}
	return nil
}

func SignAndVerifyPaymentTx(payment *PaymentReservation) error {
	sig, err := FinalSignTxWithWallet_SatsNet(payment.LocalWallet(), payment.PaymentTx, payment.PaymentPreFetcher,
		payment.Channel.RedeemScript,
		payment.Channel.GetRemotePubKey().SerializeCompressed(), payment.RemotePaymentSig)
	if err != nil {
		return err
	}
	payment.LocalPaymentSig = sig
	return VerifyPaymentTx(payment)
}
