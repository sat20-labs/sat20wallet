package wallet

import (
	"testing"

	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

func TestSplicingOperationLogDistinguishesInitiatorAndResponder(t *testing.T) {
	initiator := &SplicingReservation{}
	initiator.InitRuntime()
	initiator.IsInitiator = true
	initiator.ChannelId = "channel-1"
	initiator.FeeRate = 3
	initiator.InReq = &wwire.SplicingInRequest{Reason: SPLICING_REASON_LOCAL}
	initiator.OutReq = &wwire.SplicingOutRequest{Reason: SPLICING_REASON_LOCAL}

	in := splicingInOperationLogCreate(initiator)
	if in.Action != "splice_in" || in.Title != "Splice into channel" {
		t.Fatalf("unexpected initiator splice-in log: %+v", in)
	}
	if in.Parameters["trigger"] != "" || in.Parameters["reason"] != SPLICING_REASON_LOCAL {
		t.Fatalf("unexpected initiator splice-in parameters: %+v", in.Parameters)
	}

	out := splicingOutOperationLogCreate(initiator)
	if out.Action != "splice_out" || out.Title != "Splice out of channel" {
		t.Fatalf("unexpected initiator splice-out log: %+v", out)
	}
	if out.Parameters["trigger"] != "" || out.Parameters["reason"] != SPLICING_REASON_LOCAL {
		t.Fatalf("unexpected initiator splice-out parameters: %+v", out.Parameters)
	}

	responder := &SplicingReservation{}
	responder.InitRuntime()
	responder.IsInitiator = false
	responder.ChannelId = "channel-2"
	responder.FeeRate = 5
	responder.InReq = &wwire.SplicingInRequest{Reason: SPLICING_REASON_REMOTE}
	responder.OutReq = &wwire.SplicingOutRequest{Reason: SPLICING_REASON_REMOTE}

	in = splicingInOperationLogCreate(responder)
	if in.Action != "respond_splice_in" || in.Title != "Respond to channel splice-in" {
		t.Fatalf("unexpected responder splice-in log: %+v", in)
	}
	if in.Parameters["trigger"] != "peer_request" || in.Parameters["reason"] != SPLICING_REASON_REMOTE {
		t.Fatalf("unexpected responder splice-in parameters: %+v", in.Parameters)
	}

	out = splicingOutOperationLogCreate(responder)
	if out.Action != "respond_splice_out" || out.Title != "Respond to channel splice-out" {
		t.Fatalf("unexpected responder splice-out log: %+v", out)
	}
	if out.Parameters["trigger"] != "peer_request" || out.Parameters["reason"] != SPLICING_REASON_REMOTE {
		t.Fatalf("unexpected responder splice-out parameters: %+v", out.Parameters)
	}
}

func TestRemoteExpandOperationLogAndCompletionSemantics(t *testing.T) {
	resv := &SplicingReservation{}
	resv.InitRuntime()
	resv.Id = 77
	resv.IsInitiator = false
	resv.ChannelId = "channel-remote-expand"
	resv.FeeRate = 7
	resv.NeedSendSplicingTx = false
	resv.InReq = &wwire.SplicingInRequest{Reason: SPLICING_REASON_REMOTE}

	created := remoteExpandOperationLogCreate(resv, "funding:0")
	if created.Action != "respond_expand_channel" || created.Title != "Respond to channel expansion" {
		t.Fatalf("unexpected remote expand log: %+v", created)
	}
	if created.Parameters["trigger"] != "peer_request" || created.Parameters["utxo"] != "funding:0" ||
		created.Parameters["reason"] != SPLICING_REASON_REMOTE {
		t.Fatalf("unexpected remote expand parameters: %+v", created.Parameters)
	}

	message, details := operationLogCompletionMessage(&ActionStatusEvent{
		Resv:     resv,
		ResvType: RESV_TYPE_SPLICING,
	})
	if message != "Channel expansion completed" {
		t.Fatalf("completion message=%q", message)
	}
	if details["reservation_id"] != "77" || details["channel_id"] != resv.ChannelId {
		t.Fatalf("unexpected completion details: %+v", details)
	}
}
