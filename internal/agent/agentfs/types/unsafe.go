package types

import "github.com/sonroyaalmerol/pbs-plus/internal/arpc/arpcdata"

type UnsafeReq struct {
	Handle  uintptr
	Request []byte
}

func (req *UnsafeReq) Encode() ([]byte, error) {
	enc := arpcdata.NewEncoderWithSize(8 + 8 + 8 + 4 + 4)
	if err := enc.WriteUint64(uint64(req.Handle)); err != nil {
		return nil, err
	}
	if err := enc.WriteBytes(req.Request); err != nil {
		return nil, err
	}
	return enc.Bytes(), nil
}

func (req *UnsafeReq) Decode(buf []byte) error {
	dec, err := arpcdata.NewDecoder(buf)
	if err != nil {
		return err
	}
	handleID, err := dec.ReadUint64()
	if err != nil {
		return err
	}
	req.Handle = uintptr(handleID)
	reqEnc, err := dec.ReadBytes()
	if err != nil {
		return err
	}
	req.Request = reqEnc

	return nil
}
