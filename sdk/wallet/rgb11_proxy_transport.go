package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/invoicing"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

const (
	RGB11ProxyTransport         = "rgb-json-rpc"
	rgb11ProxyMaxEndpoints      = 8
	rgb11ProxyMaxResponseBytes  = 8 * 1024 * 1024
	rgb11ProxyMaxConsignmentLen = 4 * 1024 * 1024
)

var (
	ErrRGB11ProxyNoEndpoint    = errors.New("no supported RGB11 JSON-RPC transport endpoint")
	ErrRGB11ProxyNoConsignment = errors.New("RGB11 consignment is not available yet")
)

type rgb11ProxyEndpoint struct {
	invoice string
	url     string
}

type rgb11ProxyError struct {
	Code    int64  `json:"code"`
	Message string `json:"message"`
}

type rgb11ProxyRPCError struct {
	Code    int64
	Message string
}

func (e *rgb11ProxyRPCError) Error() string {
	return fmt.Sprintf("RGB11 proxy error %d: %s", e.Code, e.Message)
}

type rgb11ProxyResponse[T any] struct {
	Result *T               `json:"result"`
	Error  *rgb11ProxyError `json:"error"`
}

type rgb11ProxyConsignment struct {
	Consignment string  `json:"consignment"`
	TxID        string  `json:"txid"`
	Vout        *uint32 `json:"vout"`
}

type rgb11ProxyRecipientParam struct {
	RecipientID string `json:"recipient_id"`
}

type rgb11ProxyAckParam struct {
	RecipientID string `json:"recipient_id"`
	Ack         bool   `json:"ack"`
}

type rgb11ProxyPostConsignmentParam struct {
	RecipientID string  `json:"recipient_id"`
	TxID        string  `json:"txid"`
	Vout        *uint32 `json:"vout,omitempty"`
}

type rgb11ProxyRequest struct {
	Method  string `json:"method"`
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id,omitempty"`
	Params  any    `json:"params,omitempty"`
}

func rgb11ReceiveTransports(request RGB11InvoiceRequest) ([]invoicing.Transport, bool, error) {
	mode := strings.ToLower(strings.TrimSpace(request.TransportMode))
	if mode == "" || mode == "sat20" || mode == "sat20-dkvs" {
		if len(request.TransportEndpoints) != 0 {
			return nil, false, fmt.Errorf("RGB11 transport endpoints require %s mode", RGB11ProxyTransport)
		}
		return nil, false, nil
	}
	if mode == "out-of-band" {
		if len(request.TransportEndpoints) != 0 {
			return nil, false, fmt.Errorf("RGB11 out-of-band transport does not use endpoints")
		}
		return nil, true, nil
	}
	if mode != RGB11ProxyTransport && mode != "standard" {
		return nil, false, fmt.Errorf("unsupported RGB11 transport mode %q", request.TransportMode)
	}
	values := append([]string(nil), request.TransportEndpoints...)
	if len(values) == 0 {
		return nil, false, ErrRGB11ProxyNoEndpoint
	}
	if len(values) > rgb11ProxyMaxEndpoints {
		return nil, false, ErrRGB11ProxyNoEndpoint
	}
	transports := make([]invoicing.Transport, 0, len(values))
	for _, value := range values {
		transport, err := invoicing.ParseTransport(strings.TrimSpace(value))
		if err != nil {
			return nil, false, err
		}
		if _, err := rgb11ProxyEndpointURL(transport); err != nil {
			return nil, false, err
		}
		transports = append(transports, transport)
	}
	return transports, true, nil
}

func rgb11ProxyEndpoints(invoice *invoicing.Invoice) ([]rgb11ProxyEndpoint, error) {
	if invoice == nil || len(invoice.Transports) == 0 || len(invoice.Transports) > rgb11ProxyMaxEndpoints {
		return nil, ErrRGB11ProxyNoEndpoint
	}
	endpoints := make([]rgb11ProxyEndpoint, 0, len(invoice.Transports))
	seen := make(map[string]struct{}, len(invoice.Transports))
	for _, transport := range invoice.Transports {
		endpoint, err := rgb11ProxyEndpointURL(transport)
		if err != nil {
			continue
		}
		if _, ok := seen[endpoint]; ok {
			continue
		}
		seen[endpoint] = struct{}{}
		endpoints = append(endpoints, rgb11ProxyEndpoint{invoice: transport.String(), url: endpoint})
	}
	if len(endpoints) == 0 {
		return nil, ErrRGB11ProxyNoEndpoint
	}
	return endpoints, nil
}

func rgb11ProxyEndpointURL(transport invoicing.Transport) (string, error) {
	if transport.Kind != invoicing.TransportJSONRPC || strings.TrimSpace(transport.Host) == "" {
		return "", ErrRGB11ProxyNoEndpoint
	}
	scheme := "http"
	if transport.TLS {
		scheme = "https"
	}
	endpoint, err := url.Parse(scheme + "://" + transport.Host)
	if err != nil || endpoint.Hostname() == "" || endpoint.User != nil ||
		endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return "", ErrRGB11ProxyNoEndpoint
	}
	if !transport.TLS {
		host := endpoint.Hostname()
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return "", fmt.Errorf("%w: insecure remote endpoint", ErrRGB11ProxyNoEndpoint)
		}
	}
	return endpoint.String(), nil
}

func rgb11ProxyHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func rgb11ProxyJSON[T any](ctx context.Context, endpoint, method string, params any) (*T, error) {
	body, err := json.Marshal(rgb11ProxyRequest{Method: method, JSONRPC: "2.0", ID: "1", Params: params})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := rgb11ProxyHTTPClient().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("RGB11 proxy returned HTTP %d", response.StatusCode)
	}
	return decodeRGB11ProxyResponse[T](response.Body)
}

func decodeRGB11ProxyResponse[T any](reader io.Reader) (*T, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, rgb11ProxyMaxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > rgb11ProxyMaxResponseBytes {
		return nil, fmt.Errorf("RGB11 proxy response exceeds size limit")
	}
	var response rgb11ProxyResponse[T]
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("decode RGB11 proxy response: %w", err)
	}
	if response.Error != nil {
		return nil, &rgb11ProxyRPCError{Code: response.Error.Code, Message: response.Error.Message}
	}
	return response.Result, nil
}

func rgb11ProxyPostConsignment(ctx context.Context, endpoint, recipientID, txID string,
	vout *uint32, consignment []byte) error {
	if len(consignment) == 0 || len(consignment) > rgb11ProxyMaxConsignmentLen {
		return fmt.Errorf("RGB11 consignment exceeds size limit")
	}
	params, err := json.Marshal(rgb11ProxyPostConsignmentParam{
		RecipientID: recipientID, TxID: txID, Vout: vout,
	})
	if err != nil {
		return err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range map[string]string{
		"method": "consignment.post", "jsonrpc": "2.0", "id": "1", "params": string(params),
	} {
		if err := writer.WriteField(name, value); err != nil {
			return err
		}
	}
	part, err := writer.CreateFormFile("file", "consignment.rgbc")
	if err != nil {
		return err
	}
	if _, err := part.Write(consignment); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := rgb11ProxyHTTPClient().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("RGB11 proxy returned HTTP %d", response.StatusCode)
	}
	result, err := decodeRGB11ProxyResponse[bool](response.Body)
	if err != nil {
		return err
	}
	if result == nil || !*result {
		return errors.New("RGB11 proxy rejected consignment")
	}
	return nil
}

func rgb11TransferFile(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, rgb11wallet.ErrInvalidProof
	}
	container, err := coreconsignment.Decode(raw)
	if err != nil {
		return nil, err
	}
	if container.Armor == nil || container.Armor.Type != "transfer" {
		return nil, coreconsignment.ErrContainerType
	}
	return coreconsignment.EncodeFile(container)
}

func (p *rgb11Manager) publishRGB11ProxyConsignment(ctx context.Context,
	transferID string) (string, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return "", ErrRGB11Inconsistent
	}
	pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		return "", err
	}
	if pending.State.TransportMode != RGB11ProxyTransport || pending.State.Expiry <= time.Now().Unix() {
		return "", ErrRGB11ProxyNoEndpoint
	}
	invoice, endpoints, err := rgb11ProxyInvoice(pending.State.Invoice)
	if err != nil {
		return "", err
	}
	if pending.State.Status == "relayed" || pending.State.Status == "broadcast" ||
		pending.State.Status == "settled" {
		return endpoints[0].invoice, nil
	}
	consignment, err := rgb11TransferFile(pending.RecipientConsignment)
	if err != nil {
		return "", err
	}
	var vout *uint32
	if invoice.Beneficiary.Kind == invoicing.BeneficiaryWitnessVout {
		value := pending.State.RecipientVout
		vout = &value
	}
	var attempts []error
	for _, endpoint := range endpoints {
		err := rgb11ProxyPostConsignment(
			ctx, endpoint.url, pending.State.RecipientID, pending.State.WitnessTxID, vout, consignment,
		)
		if err != nil {
			attempts = append(attempts, fmt.Errorf("%s: %w", endpoint.invoice, err))
			continue
		}
		pending.State.Status = "relayed"
		pending.State.RelayDurability = "STANDARD_PROXY"
		if err := p.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
			return "", err
		}
		p.autoBackupRGB11AfterMutation()
		return endpoint.invoice, nil
	}
	return "", errors.Join(attempts...)
}

func (p *rgb11Manager) ReceiveRGB11ProxyConsignment(ctx context.Context,
	requestID string) (*RGB11ProxyReceiveResult, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.engine == nil {
		return nil, ErrRGB11Inconsistent
	}
	request, err := p.rgbManager.engine.LoadReceive(requestID)
	if err != nil {
		return nil, err
	}
	invoice, endpoints, err := rgb11ProxyInvoice(request.Invoice)
	if err != nil {
		return nil, err
	}
	recipientID := invoice.Beneficiary.String()
	if request.Status == corewallet.ReceiveAccepted {
		txID := request.WitnessTxID
		if state, loadErr := p.rgbManager.projectionStore.LoadTransferState(request.TransferID); loadErr == nil {
			txID = state.WitnessTxID
		}
		for _, endpoint := range endpoints {
			if err := rgb11ProxyEnsureAck(ctx, endpoint.url, recipientID, true); err == nil {
				return &RGB11ProxyReceiveResult{
					RequestID: requestID, Endpoint: endpoint.invoice, TxID: txID, AckPosted: true,
				}, nil
			}
		}
		return nil, errors.New("failed to retry RGB11 proxy acknowledgment")
	}
	if request.Status == corewallet.ReceiveAcknowledged {
		raw, loadErr := p.rgbManager.projectionStore.LoadObject(request.ObjectHash)
		if loadErr != nil {
			return nil, loadErr
		}
		receipt, acceptErr := p.acceptRGB11Consignment(ctx, requestID, raw, true, request.WitnessTxID, nil)
		if acceptErr != nil {
			if !errors.Is(acceptErr, coreconsignment.ErrWitnessUnresolved) &&
				!errors.Is(acceptErr, coreconsignment.ErrOutpointUnknown) {
				for _, endpoint := range endpoints {
					rgb11ProxyPostNackIfTerminal(ctx, endpoint.url, recipientID, acceptErr)
				}
				return nil, p.finishRGB11ProxyTerminal(requestID, raw, acceptErr)
			}
			for _, endpoint := range endpoints {
				if err := rgb11ProxyEnsureAck(ctx, endpoint.url, recipientID, true); err == nil {
					state, _ := p.rgbManager.projectionStore.LoadTransferState(request.TransferID)
					var vout *uint32
					if state != nil && len(state.OutputOutPoints) > 0 {
						value, ok := outpointVout(state.OutputOutPoints[0])
						if ok {
							vout = &value
						}
					}
					return &RGB11ProxyReceiveResult{
						RequestID: requestID, Endpoint: endpoint.invoice,
						TxID: request.WitnessTxID, Vout: vout, AckPosted: true,
						AwaitingBroadcast: true,
					}, nil
				}
			}
			return nil, errors.New("failed to retry RGB11 proxy acknowledgment")
		}
		actualTxID := request.WitnessTxID
		var actualVout *uint32
		if state, loadErr := p.rgbManager.projectionStore.LoadTransferState(receipt.TransferID); loadErr == nil {
			actualTxID = state.WitnessTxID
			if len(state.OutputOutPoints) == 1 {
				if value, ok := outpointVout(state.OutputOutPoints[0]); ok &&
					invoice.Beneficiary.Kind == invoicing.BeneficiaryWitnessVout {
					actualVout = &value
				}
			}
		}
		for _, endpoint := range endpoints {
			if err := rgb11ProxyEnsureAck(ctx, endpoint.url, recipientID, true); err == nil {
				return &RGB11ProxyReceiveResult{
					RequestID: requestID, Endpoint: endpoint.invoice, TxID: actualTxID,
					Vout: actualVout, Receipt: receipt, AckPosted: true,
				}, nil
			}
		}
		return nil, errors.New("failed to retry RGB11 proxy acknowledgment")
	}
	var attempts []error
	for _, endpoint := range endpoints {
		remote, err := rgb11ProxyJSON[rgb11ProxyConsignment](
			ctx, endpoint.url, "consignment.get", rgb11ProxyRecipientParam{RecipientID: recipientID},
		)
		if err != nil {
			attempts = append(attempts, fmt.Errorf("%s: %w", endpoint.invoice, err))
			continue
		}
		if remote == nil || strings.TrimSpace(remote.Consignment) == "" {
			attempts = append(attempts, fmt.Errorf("%s: %w", endpoint.invoice, ErrRGB11ProxyNoConsignment))
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(remote.Consignment)
		if err != nil || len(raw) == 0 || len(raw) > rgb11ProxyMaxConsignmentLen {
			_ = rgb11ProxyPostAck(ctx, endpoint.url, recipientID, false)
			validationErr := errors.New("RGB11 proxy returned an invalid consignment")
			return nil, p.finishRGB11ProxyTerminal(requestID, raw, validationErr)
		}
		preflight, err := p.ValidateRGB11Consignment(ctx, raw)
		if err != nil {
			if errors.Is(err, coreconsignment.ErrWitnessUnresolved) ||
				errors.Is(err, coreconsignment.ErrOutpointUnknown) {
				receipt, preparedErr := p.prepareRGB11Consignment(
					ctx, requestID, raw, remote.TxID, remote.Vout, true,
				)
				if preparedErr != nil {
					rgb11ProxyPostNackIfTerminal(ctx, endpoint.url, recipientID, preparedErr)
					return nil, p.finishRGB11ProxyTerminal(requestID, raw, preparedErr)
				}
				if err := rgb11ProxyEnsureAck(ctx, endpoint.url, recipientID, true); err != nil {
					return nil, err
				}
				return &RGB11ProxyReceiveResult{
					RequestID: requestID, Endpoint: endpoint.invoice, TxID: remote.TxID,
					Vout: remote.Vout, Receipt: receipt, AckPosted: true,
					AwaitingBroadcast: true,
				}, nil
			}
			rgb11ProxyPostNackIfTerminal(ctx, endpoint.url, recipientID, err)
			return nil, p.finishRGB11ProxyTerminal(requestID, raw, err)
		}
		if !validRGB11TxID(remote.TxID) ||
			(invoice.Beneficiary.Kind == invoicing.BeneficiaryWitnessVout && remote.Vout == nil) {
			_ = rgb11ProxyPostAck(ctx, endpoint.url, recipientID, false)
			validationErr := errors.New("RGB11 proxy metadata does not match the accepted consignment")
			return nil, p.finishRGB11ProxyTerminal(requestID, raw, validationErr)
		}
		matched, err := p.matchRGB11ReceiveAllocation(
			preflight, request, invoice, nil, remote.TxID, remote.Vout,
		)
		if err != nil || matched.TxID != remote.TxID ||
			(invoice.Beneficiary.Kind == invoicing.BeneficiaryWitnessVout &&
				(matched.Vout == nil || *matched.Vout != *remote.Vout)) {
			_ = rgb11ProxyPostAck(ctx, endpoint.url, recipientID, false)
			validationErr := errors.New("RGB11 proxy metadata does not match the accepted consignment")
			if err != nil {
				validationErr = fmt.Errorf("%w: %v", validationErr, err)
			}
			return nil, p.finishRGB11ProxyTerminal(requestID, raw, validationErr)
		}
		receipt, err := p.acceptRGB11Consignment(
			ctx, requestID, raw, true, remote.TxID, remote.Vout,
		)
		if err != nil {
			rgb11ProxyPostNackIfTerminal(ctx, endpoint.url, recipientID, err)
			return nil, p.finishRGB11ProxyTerminal(requestID, raw, err)
		}
		if err := rgb11ProxyEnsureAck(ctx, endpoint.url, recipientID, true); err != nil {
			return nil, err
		}
		return &RGB11ProxyReceiveResult{
			RequestID: requestID, Endpoint: endpoint.invoice, TxID: matched.TxID,
			Vout: matched.Vout, Receipt: receipt, AckPosted: true,
		}, nil
	}
	if len(attempts) == 0 {
		return nil, ErrRGB11ProxyNoConsignment
	}
	return nil, errors.Join(attempts...)
}

func (p *rgb11Manager) finishRGB11ProxyTerminal(requestID string, raw []byte, validationErr error) error {
	if p == nil || p.rgbManager == nil || p.rgbManager.engine == nil ||
		p.rgbManager.projectionStore == nil {
		return validationErr
	}
	var cleanupErr error
	if err := p.releaseRGB11ReceiveReservation(requestID); err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	}
	request, err := p.rgbManager.engine.LoadReceive(requestID)
	if err != nil {
		return errors.Join(validationErr, cleanupErr, err)
	}
	transferID := request.TransferID
	if transferID == "" && len(raw) != 0 {
		if container, decodeErr := coreconsignment.Decode(raw); decodeErr == nil && container.Armor != nil {
			transferID = container.Armor.ID
		}
	}
	objectHash := sha256.Sum256(raw)
	if transferID != "" && len(raw) != 0 {
		if err := p.rgbManager.engine.MarkRelayRejected(
			requestID, transferID, hex.EncodeToString(objectHash[:]), "validation-failed",
		); err != nil && !errors.Is(err, corewallet.ErrReceiveState) {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	if request.TransferID != "" {
		if state, loadErr := p.rgbManager.projectionStore.LoadTransferState(request.TransferID); loadErr == nil {
			state.AckStatus = "rejected"
			state.Status = "rejected"
			state.RejectReason = "validation-failed"
			locked := p.utxoLockerL1.GetLockedUtxoList()
			for _, outpoint := range state.OutputOutPoints {
				if lock := locked[outpoint]; lock != nil && lock.Reason == rgb11wallet.LockReasonPending {
					if err := p.utxoLockerL1.UnlockUtxo(outpoint); err != nil {
						cleanupErr = errors.Join(cleanupErr, err)
					}
				}
			}
			if err := p.rgbManager.projectionStore.SaveTransferState(state); err != nil {
				cleanupErr = errors.Join(cleanupErr, err)
			}
			if err := p.rgbManager.projectionStore.DeletePreparedReceive(request.TransferID); err != nil &&
				!errors.Is(err, indexer.ErrKeyNotFound) {
				cleanupErr = errors.Join(cleanupErr, err)
			}
		}
	}
	return errors.Join(validationErr, cleanupErr)
}

func rgb11ProxyPostNackIfTerminal(ctx context.Context, endpoint, recipientID string, err error) {
	if errors.Is(err, coreconsignment.ErrWitnessUnresolved) ||
		errors.Is(err, coreconsignment.ErrOutpointUnknown) {
		return
	}
	_ = rgb11ProxyPostAck(ctx, endpoint, recipientID, false)
}

func rgb11ProxyPostAck(ctx context.Context, endpoint, recipientID string, accepted bool) error {
	result, err := rgb11ProxyJSON[bool](
		ctx, endpoint, "ack.post", rgb11ProxyAckParam{RecipientID: recipientID, Ack: accepted},
	)
	if err != nil {
		return err
	}
	if result == nil || !*result {
		return errors.New("RGB11 proxy rejected acknowledgment")
	}
	return nil
}

func rgb11ProxyEnsureAck(ctx context.Context, endpoint, recipientID string, accepted bool) error {
	postErr := rgb11ProxyPostAck(ctx, endpoint, recipientID, accepted)
	if postErr == nil {
		return nil
	}
	decision, getErr := rgb11ProxyJSON[bool](
		ctx, endpoint, "ack.get", rgb11ProxyRecipientParam{RecipientID: recipientID},
	)
	if getErr != nil {
		return errors.Join(postErr, fmt.Errorf("query RGB11 proxy acknowledgment: %w", getErr))
	}
	if decision == nil {
		return errors.Join(postErr, errors.New("RGB11 proxy acknowledgment is not available"))
	}
	if *decision != accepted {
		return errors.Join(postErr, errors.New("RGB11 proxy acknowledgment conflicts with requested decision"))
	}
	return nil
}

func (p *rgb11Manager) DeliverAndBroadcastRGB11ProxyTransfer(ctx context.Context,
	transferIDs []string) (*RGB11ProxyDeliveryResult, error) {
	pendingList, err := p.loadRGB11ProxyPendingBatch(transferIDs)
	if err != nil {
		return nil, err
	}
	first := pendingList[0]
	allBroadcast := true
	for _, pending := range pendingList {
		if pending.State.Status != "broadcast" && pending.State.Status != "settled" {
			allBroadcast = false
			break
		}
	}
	if allBroadcast {
		return &RGB11ProxyDeliveryResult{
			TransferIDs: append([]string(nil), transferIDs...),
			TxID:        first.State.WitnessTxID,
		}, nil
	}
	endpoints := make([]string, 0, len(transferIDs))
	for _, transferID := range transferIDs {
		endpoint, err := p.publishRGB11ProxyConsignment(ctx, transferID)
		if err != nil {
			var proxyErr *rgb11ProxyRPCError
			if errors.As(err, &proxyErr) && proxyErr.Code == -101 {
				if cancelErr := p.cancelRGB11PendingBatch(
					pendingList, "proxy-recipient-conflict", nil,
				); cancelErr != nil {
					return nil, errors.Join(err, cancelErr)
				}
			}
			return nil, err
		}
		endpoints = append(endpoints, endpoint)
	}
	if err := p.requireLatestRGB11WalletState(); err != nil {
		return nil, err
	}
	txID, err := p.rgbManager.evidence.Broadcast(first.SignedTx)
	if err != nil {
		return nil, err
	}
	if txID != "" && txID != first.State.WitnessTxID {
		return nil, fmt.Errorf("RGB11 backend returned witness txid %s, expected %s", txID, first.State.WitnessTxID)
	}
	pendingList, err = p.loadRGB11ProxyPendingBatch(transferIDs)
	if err != nil {
		return nil, err
	}
	for _, pending := range pendingList {
		pending.State.Status = "broadcast"
		pending.State.AckStatus = "awaiting"
	}
	if err := p.rgbManager.projectionStore.SavePendingTransferStates(pendingList); err != nil {
		return nil, err
	}
	p.autoBackupRGB11AfterMutation()
	return &RGB11ProxyDeliveryResult{
		TransferIDs: append([]string(nil), transferIDs...),
		Endpoints:   endpoints,
		TxID:        first.State.WitnessTxID,
	}, nil
}

func (p *rgb11Manager) FetchRGB11ProxyAck(ctx context.Context,
	transferID string) (*RGB11ProxyAckResult, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return nil, ErrRGB11Inconsistent
	}
	pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		return nil, err
	}
	if pending.State.TransportMode != RGB11ProxyTransport {
		return nil, ErrRGB11ProxyNoEndpoint
	}
	_, endpoints, err := rgb11ProxyInvoice(pending.State.Invoice)
	if err != nil {
		return nil, err
	}
	var attempts []error
	for _, endpoint := range endpoints {
		decision, err := rgb11ProxyJSON[bool](
			ctx, endpoint.url, "ack.get",
			rgb11ProxyRecipientParam{RecipientID: pending.State.RecipientID},
		)
		if err != nil {
			attempts = append(attempts, fmt.Errorf("%s: %w", endpoint.invoice, err))
			continue
		}
		result := &RGB11ProxyAckResult{
			TransferID: transferID,
			Available:  decision != nil,
			Endpoint:   endpoint.invoice,
		}
		if decision == nil {
			return result, nil
		}
		result.Accepted = *decision
		if *decision {
			pending.State.AckStatus = "accepted"
		} else {
			pending.State.AckStatus = "rejected-after-broadcast"
		}
		if err := p.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
			return nil, err
		}
		p.autoBackupRGB11AfterMutation()
		return result, nil
	}
	if len(attempts) == 0 {
		return &RGB11ProxyAckResult{TransferID: transferID}, nil
	}
	return nil, errors.Join(attempts...)
}

func (p *rgb11Manager) loadRGB11ProxyPendingBatch(
	transferIDs []string) ([]*rgb11wallet.PendingTransfer, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil ||
		p.rgbManager.evidence == nil || len(transferIDs) == 0 {
		return nil, ErrRGB11BatchAckRequired
	}
	pendingList := make([]*rgb11wallet.PendingTransfer, 0, len(transferIDs))
	seen := make(map[string]struct{}, len(transferIDs))
	for _, transferID := range transferIDs {
		if _, ok := seen[transferID]; transferID == "" || ok {
			return nil, ErrRGB11BatchAckRequired
		}
		seen[transferID] = struct{}{}
		pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
		if err != nil {
			return nil, err
		}
		pendingList = append(pendingList, pending)
	}
	first := pendingList[0]
	expected := first.State.BatchTransferIDs
	if len(expected) == 0 {
		expected = []string{first.State.TransferID}
	}
	if len(expected) != len(transferIDs) {
		return nil, ErrRGB11BatchAckRequired
	}
	for _, transferID := range expected {
		if _, ok := seen[transferID]; !ok {
			return nil, ErrRGB11BatchAckRequired
		}
	}
	for _, pending := range pendingList {
		if pending.State.TransportMode != RGB11ProxyTransport ||
			pending.State.Expiry <= time.Now().Unix() ||
			pending.State.WitnessTxID != first.State.WitnessTxID ||
			pending.State.BatchID != first.State.BatchID ||
			!bytes.Equal(pending.SignedTx, first.SignedTx) {
			return nil, ErrRGB11BatchAckRequired
		}
	}
	return pendingList, nil
}

func rgb11ProxyInvoice(raw string) (*invoicing.Invoice, []rgb11ProxyEndpoint, error) {
	invoice, err := invoicing.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, nil, err
	}
	if invoice.Beneficiary.Kind != invoicing.BeneficiaryWitnessVout &&
		invoice.Beneficiary.Kind != invoicing.BeneficiaryBlindedSeal {
		return nil, nil, invoicing.ErrInvalidInvoice
	}
	endpoints, err := rgb11ProxyEndpoints(invoice)
	return invoice, endpoints, err
}

func validRGB11TxID(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}
