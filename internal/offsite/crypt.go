package offsite

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
)

// An encrypted object is the magic, then chunks of up to chunkSize bytes, each sealed with
// XChaCha20-Poly1305 under a fresh random nonce (stored in front of it). The chunk's index
// and a final-chunk flag are authenticated, so reordering or truncating an object fails to
// decrypt. Chunking keeps memory flat for multi-gigabyte volumes.
const (
	magic     = "KAYLY1"
	chunkSize = 1 << 20
	overhead  = chacha20poly1305.NonceSizeX + chacha20poly1305.Overhead
)

// KeySize is the size of the recovery key.
const KeySize = chacha20poly1305.KeySize

// EncryptedSize is the size of Encrypt's output for n bytes of input, known up front so an
// upload can stream without a temporary file.
func EncryptedSize(n int64) int64 {
	chunks := max((n+chunkSize-1)/chunkSize, 1)
	return int64(len(magic)) + n + chunks*overhead
}

func chunkAD(index uint64, final bool) []byte {
	ad := binary.BigEndian.AppendUint64(nil, index)
	if final {
		return append(ad, 1)
	}
	return append(ad, 0)
}

// Encrypt reads r to the end and writes the encrypted stream to w.
func Encrypt(w io.Writer, r io.Reader, key []byte) error {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, magic); err != nil {
		return err
	}
	br := bufio.NewReaderSize(r, chunkSize+1)
	buf := make([]byte, chunkSize)
	for index := uint64(0); ; index++ {
		n, err := io.ReadFull(br, buf)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			return err
		}
		_, peek := br.Peek(1)
		final := peek != nil // nothing follows this chunk
		nonce := make([]byte, aead.NonceSize(), overhead+n)
		rand.Read(nonce)
		if _, err := w.Write(aead.Seal(nonce, nonce, buf[:n], chunkAD(index, final))); err != nil {
			return err
		}
		if final {
			return nil
		}
	}
}

// Decrypt reads an encrypted stream from r and writes the plaintext to w. It fails on a
// wrong key, a corrupted chunk or a truncated stream.
func Decrypt(w io.Writer, r io.Reader, key []byte) error {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return err
	}
	br := bufio.NewReaderSize(r, chunkSize+overhead+1)
	head := make([]byte, len(magic))
	if _, err := io.ReadFull(br, head); err != nil || string(head) != magic {
		return errors.New("offsite: not an encrypted Kayly backup")
	}
	buf := make([]byte, chunkSize+overhead)
	for index := uint64(0); ; index++ {
		n, err := io.ReadFull(br, buf)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("offsite: truncated backup: %w", err)
		}
		_, peek := br.Peek(1)
		final := peek != nil
		if n < overhead {
			return errors.New("offsite: truncated backup")
		}
		plain, err := aead.Open(nil, buf[:aead.NonceSize()], buf[aead.NonceSize():n], chunkAD(index, final))
		if err != nil {
			return errors.New("offsite: cannot decrypt (wrong recovery key or damaged backup)")
		}
		if _, err := w.Write(plain); err != nil {
			return err
		}
		if final {
			return nil
		}
	}
}
