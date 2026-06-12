package node

import (
	"errors"
	"fmt"
	"io"

	"kray/pkg/relay"
)

type RelayTarget = relay.Address

func writeConnectRequest(w io.Writer, target RelayTarget) error {
	return relay.WriteRequest(w, relay.TCPConnect(target.Host, target.Port))
}

func readRelayRequest(r io.Reader) (relay.Request, error) {
	return relay.ReadRequest(r)
}

func readConnectRequest(r io.Reader) (RelayTarget, error) {
	req, err := relay.ReadRequest(r)
	if err != nil {
		return RelayTarget{}, err
	}
	if req.Command != relay.CommandTCPConnect {
		return RelayTarget{}, fmt.Errorf("%w: %d", relay.ErrUnsupportedCommand, req.Command)
	}
	return req.Address, nil
}

func writeConnectResponse(w io.Writer, errText string) error {
	if errText == "" {
		return relay.WriteResponse(w, relay.Response{Status: relay.StatusOK})
	}
	return relay.WriteResponse(w, relay.Response{Status: relay.StatusDialFailed, Message: errText})
}

func readConnectResponse(r io.Reader) error {
	resp, err := relay.ReadResponse(r)
	if err != nil {
		return err
	}
	if resp.Status == relay.StatusOK {
		return nil
	}
	if resp.Message == "" {
		return errors.New("relay connection rejected")
	}
	return errors.New(resp.Message)
}
