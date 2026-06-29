package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"time"
)

const (
	miioPort = 54321
	maxMsg   = 10240
)

type miioRequest struct {
	ID     int         `json:"id"`
	Method string      `json:"method"`
	Params interface{} `json:"params"`
}

type Device struct {
	host     string
	token    []byte
	deviceID uint32
	deltaTS  float64
	key      []byte
	iv       []byte
	timeout  time.Duration
}

func NewDevice(host, token string, timeout time.Duration) (*Device, error) {
	if len(token) != 32 {
		return nil, fmt.Errorf("token must be 32 hex characters")
	}
	tok := make([]byte, 16)
	if n, err := fmt.Sscanf(token, "%32x", &tok); err != nil || n != 1 {
		return nil, fmt.Errorf("invalid token: must be 32 hex characters")
	}

	hash := md5.Sum(tok)
	key := hash[:]
	ivInput := append(key, tok...)
	ivHash := md5.Sum(ivInput)
	iv := ivHash[:]

	return &Device{
		host:    host,
		token:   tok,
		key:     key,
		iv:      iv,
		timeout: timeout,
	}, nil
}

func (d *Device) encrypt(plaintext []byte) []byte {
	block, _ := aes.NewCipher(d.key)
	blockSize := block.BlockSize()
	padding := blockSize - len(plaintext)%blockSize
	padded := make([]byte, len(plaintext)+padding)
	copy(padded, plaintext)
	for i := len(plaintext); i < len(padded); i++ {
		padded[i] = byte(padding)
	}
	ciphertext := make([]byte, len(padded))
	mode := cipher.NewCBCEncrypter(block, d.iv)
	mode.CryptBlocks(ciphertext, padded)
	return ciphertext
}

func (d *Device) decrypt(ciphertext []byte) ([]byte, error) {
	block, _ := aes.NewCipher(d.key)
	plaintext := make([]byte, len(ciphertext))
	mode := cipher.NewCBCDecrypter(block, d.iv)
	mode.CryptBlocks(plaintext, ciphertext)
	if len(plaintext) == 0 {
		return nil, fmt.Errorf("empty decrypted data")
	}
	padding := int(plaintext[len(plaintext)-1])
	if padding > len(plaintext) || padding == 0 {
		return nil, fmt.Errorf("invalid padding")
	}
	for _, b := range plaintext[len(plaintext)-padding:] {
		if b != byte(padding) {
			return nil, fmt.Errorf("invalid padding bytes")
		}
	}
	return plaintext[:len(plaintext)-padding], nil
}

func (d *Device) packMessage(method string, params interface{}) ([]byte, error) {
	msgID := rand.Intn(900000000) + 100000000
	req := miioRequest{
		ID:     msgID,
		Method: method,
		Params: params,
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	payload = append(payload, 0)

	encrypted := d.encrypt(payload)

	var buf bytes.Buffer
	buf.Write([]byte{0x21, 0x31})
	totalLen := uint16(32 + len(encrypted))
	binary.Write(&buf, binary.BigEndian, totalLen)
	buf.Write([]byte{0x00, 0x00, 0x00, 0x00})
	binary.Write(&buf, binary.BigEndian, d.deviceID)
	binTS := uint32(time.Now().Unix() - int64(d.deltaTS))
	binary.Write(&buf, binary.BigEndian, binTS)

	header := buf.Bytes()
	sumInput := make([]byte, 0, len(header)+len(d.token)+len(encrypted))
	sumInput = append(sumInput, header...)
	sumInput = append(sumInput, d.token...)
	sumInput = append(sumInput, encrypted...)
	checksum := md5.Sum(sumInput)
	buf.Write(checksum[:])
	buf.Write(encrypted)

	return buf.Bytes(), nil
}

func (d *Device) unpackResponse(raw []byte) ([]byte, error) {
	if len(raw) < 32 {
		return nil, fmt.Errorf("response too short")
	}
	if raw[0] != 0x21 || raw[1] != 0x31 {
		return nil, fmt.Errorf("invalid response header")
	}
	return d.decrypt(raw[32:])
}

func (d *Device) hello(conn *net.UDPConn) error {
	hello := []byte{
		0x21, 0x31, 0x00, 0x20,
		0xff, 0xff, 0xff, 0xff,
		0xff, 0xff, 0xff, 0xff,
		0xff, 0xff, 0xff, 0xff,
		0xff, 0xff, 0xff, 0xff,
		0xff, 0xff, 0xff, 0xff,
		0xff, 0xff, 0xff, 0xff,
		0xff, 0xff, 0xff, 0xff,
	}
	if _, err := conn.Write(hello); err != nil {
		return err
	}
	buf := make([]byte, 32)
	if err := conn.SetReadDeadline(time.Now().Add(d.timeout)); err != nil {
		return err
	}
	n, err := conn.Read(buf)
	if err != nil {
		return err
	}
	buf = buf[:n]
	if len(buf) < 16 || buf[0] != 0x21 || buf[1] != 0x31 {
		return fmt.Errorf("invalid hello response")
	}
	d.deviceID = binary.BigEndian.Uint32(buf[8:12])
	ts := binary.BigEndian.Uint32(buf[12:16])
	d.deltaTS = float64(time.Now().Unix()) - float64(ts)
	return nil
}

func (d *Device) Send(method string, params interface{}) (json.RawMessage, error) {
	var lastErr error
	pings := 0
	for try := 0; try < 3; try++ {
		addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(d.host, fmt.Sprint(miioPort)))
		if err != nil {
			return nil, err
		}
		conn, err := net.DialUDP("udp", nil, addr)
		if err != nil {
			return nil, err
		}

		if d.deltaTS == 0 {
			if err := d.hello(conn); err != nil {
				conn.Close()
				lastErr = err
				pings++
				continue
			}
		}

		raw, err := d.packMessage(method, params)
		if err != nil {
			conn.Close()
			return nil, err
		}

		if _, err := conn.Write(raw); err != nil {
			conn.Close()
			lastErr = err
			d.deltaTS = 0
			continue
		}

		if err := conn.SetReadDeadline(time.Now().Add(d.timeout)); err != nil {
			conn.Close()
			return nil, err
		}
		buf := make([]byte, maxMsg)
		n, err := conn.Read(buf)
		conn.Close()
		if err != nil {
			lastErr = err
			d.deltaTS = 0
			continue
		}

		data, err := d.unpackResponse(buf[:n])
		if err != nil {
			lastErr = err
			d.deltaTS = 0
			continue
		}
		data = bytes.TrimRight(data, "\x00")

		if string(data) == "" {
			return json.RawMessage(`{"result":""}`), nil
		}

		return json.RawMessage(data), nil
	}
	if pings >= 2 {
		return nil, fmt.Errorf("device offline")
	}
	return nil, lastErr
}

func (d *Device) Info() (map[string]interface{}, bool, error) {
	raw, err := d.Send("miIO.info", []interface{}{})
	if err != nil {
		if d.deviceID == 0 {
			return nil, false, err
		}
		return nil, true, nil
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, true, nil
	}
	if resp.Error != nil {
		return nil, true, nil
	}
	if len(resp.Result) == 0 || string(resp.Result) == `""` {
		return map[string]interface{}{}, false, nil
	}
	var info map[string]interface{}
	if err := json.Unmarshal(resp.Result, &info); err != nil {
		return nil, true, nil
	}
	return info, false, nil
}
