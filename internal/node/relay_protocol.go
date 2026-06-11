package node

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
)

const (
	relayVersion   byte = 1
	commandConnect byte = 1
	addrIPv4       byte = 1
	addrDomain     byte = 3
	addrIPv6       byte = 4

	maxDomainLength = 255
)

var relayMagic = [4]byte{'K', 'R', 'Y', 'N'}

type RelayTarget struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
}

func (t RelayTarget) Address() string {
	return net.JoinHostPort(t.Host, fmt.Sprintf("%d", t.Port))
}

func writeConnectRequest(w io.Writer, target RelayTarget) error {
	if _, err := w.Write(relayMagic[:]); err != nil {
		return err
	}
	if _, err := w.Write([]byte{relayVersion, commandConnect}); err != nil {
		return err
	}
	ip := net.ParseIP(target.Host)
	ip4 := ip.To4()
	ip16 := ip.To16()
	switch {
	case ip4 != nil:
		if _, err := w.Write([]byte{addrIPv4}); err != nil {
			return err
		}
		if _, err := w.Write(ip4); err != nil {
			return err
		}
	case ip16 != nil:
		if _, err := w.Write([]byte{addrIPv6}); err != nil {
			return err
		}
		if _, err := w.Write(ip16); err != nil {
			return err
		}
	default:
		if len(target.Host) == 0 || len(target.Host) > maxDomainLength {
			return errors.New("domain length must be between 1 and 255 bytes")
		}
		if _, err := w.Write([]byte{addrDomain, byte(len(target.Host))}); err != nil {
			return err
		}
		if _, err := io.WriteString(w, target.Host); err != nil {
			return err
		}
	}
	var port [2]byte
	binary.BigEndian.PutUint16(port[:], target.Port)
	_, err := w.Write(port[:])
	return err
}

func readConnectRequest(r io.Reader) (RelayTarget, error) {
	var header [7]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return RelayTarget{}, err
	}
	if string(header[:4]) != string(relayMagic[:]) {
		return RelayTarget{}, errors.New("invalid relay magic")
	}
	if header[4] != relayVersion {
		return RelayTarget{}, fmt.Errorf("unsupported relay version %d", header[4])
	}
	if header[5] != commandConnect {
		return RelayTarget{}, fmt.Errorf("unsupported relay command %d", header[5])
	}

	var host string
	switch header[6] {
	case addrIPv4:
		var raw [4]byte
		if _, err := io.ReadFull(r, raw[:]); err != nil {
			return RelayTarget{}, err
		}
		host = net.IP(raw[:]).String()
	case addrIPv6:
		var raw [16]byte
		if _, err := io.ReadFull(r, raw[:]); err != nil {
			return RelayTarget{}, err
		}
		host = net.IP(raw[:]).String()
	case addrDomain:
		var size [1]byte
		if _, err := io.ReadFull(r, size[:]); err != nil {
			return RelayTarget{}, err
		}
		if size[0] == 0 {
			return RelayTarget{}, errors.New("empty domain")
		}
		raw := make([]byte, int(size[0]))
		if _, err := io.ReadFull(r, raw); err != nil {
			return RelayTarget{}, err
		}
		host = string(raw)
	default:
		return RelayTarget{}, fmt.Errorf("unsupported address type %d", header[6])
	}
	var port [2]byte
	if _, err := io.ReadFull(r, port[:]); err != nil {
		return RelayTarget{}, err
	}
	return RelayTarget{Host: host, Port: binary.BigEndian.Uint16(port[:])}, nil
}

func writeConnectResponse(w io.Writer, errText string) error {
	if errText == "" {
		_, err := w.Write([]byte{0, 0})
		return err
	}
	if len(errText) > 255 {
		errText = errText[:255]
	}
	if _, err := w.Write([]byte{1, byte(len(errText))}); err != nil {
		return err
	}
	_, err := io.WriteString(w, errText)
	return err
}

func readConnectResponse(r io.Reader) error {
	var header [2]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	if header[0] == 0 {
		return nil
	}
	if header[1] == 0 {
		return errors.New("relay connection rejected")
	}
	raw := make([]byte, int(header[1]))
	if _, err := io.ReadFull(r, raw); err != nil {
		return err
	}
	return errors.New(string(raw))
}
