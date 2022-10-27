// crypto.go is an example to provides basic cryptographical functions for
// aeacus.
//
// This file is not a good example of cryptographic security. However, with this
// architecture of application (see security.md), it's good enough.
//
// Practically, it is more important that your implemented solution is different
// than the example, to make reverse engineering more difficult.
//
// You could change this file each time you release an image, which would make
// things more difficult for a would-be hacker.
//
// If you compile the source code yourself, using the Makefile, random strings
// will be generated for you. This means that the pre-compiled release will no
// longer work for decrypting your configs, which is good.

package main

import (
	"bytes"
	"compress/zlib"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"
)

// This string will be used for XORing the plaintext.
// Again-- not cryptographically genius.
//
// This string will be autogenerated if you run `make release`, or the shell
// script at `misc/dev/gen-crypto.sh`.
const (
	randomString = "HASH_HERE"
)

// byteKey is used as the key for AES encryption. It will be autogenerated
// upon running `make release`, or the script at `misc/dev/gen-crypto.sh`.
var byteKey = []byte{0x01}

// randomBytes are used to obfuscate the config values. It will be auto-
// generated like the above two values.
var randomBytes = []byte{1}

// encryptConfig takes a plaintext string and returns an encrypted string that
// should be written to the encrypted scoring data file.
func encryptConfig(plainText string) (string, error) {
	var key string

	// If string is short, generate key by hashing string
	if len(randomString) < 64 {
		hasher := sha256.New()
		_, err := hasher.Write([]byte(randomString))
		if err != nil {
			fail(err)
			return "", err
		}
		key = string(hasher.Sum(nil))
	} else {
		key = randomString
	}

	// Compress the file with zlib
	var compressedFile bytes.Buffer
	writer := zlib.NewWriter(&compressedFile)

	// Write zlib compressed data into encryptedFile
	_, err := writer.Write([]byte(plainText))
	if err != nil {
		return "", err
	}
	writer.Close()

	// XOR the file content with our key
	xorConfig := xor(key, compressedFile.String())

	// Return the AES-GCM encrypted file content
	return encryptString(string(byteKey), xorConfig), nil
}

// decryptConfig is used to decrypt the scoring data file.
func decryptConfig(cipherText string) (string, error) {
	var key string

	// If string is short, generate key by hashing string
	if len(randomString) < 64 {
		hasher := sha256.New()
		_, err := hasher.Write([]byte(randomString))
		if err != nil {
			fail(err)
			return "", err
		}
		key = string(hasher.Sum(nil))
	} else {
		key = randomString
	}

	// Decrypt with AES-GCM and the byteKey
	cipherText = decryptString(string(byteKey), cipherText)

	// Apply the XOR key to get the zlib-compressed data.
	cipherText = xor(key, cipherText)

	// Create the zlib reader
	reader, err := zlib.NewReader(bytes.NewReader([]byte(cipherText)))
	if err != nil {
		return "", errors.New("error creating zlib reader")
	}
	defer reader.Close()

	// Read into our created buffer
	dataBuffer := bytes.NewBuffer(nil)

	// HACK
	//
	// For some reason, when we use zlib in combination with AES-GCM, zlib throws
	// an unexpected EOF when the EOF is very expected. The hack right now is to
	// just ignore errors if it's an unexpected EOF.
	//
	// Likely related: https://github.com/golang/go/issues/14675
	_, err = io.Copy(dataBuffer, reader)
	if err != nil {
		if err.Error() == "unexpected EOF" {
			debug("zlib returned unexpected EOF")
			err = nil
		} else {
			return "", errors.New("error decrypting or decompressing zlib data: " + err.Error())
		}
	}

	// Sanity check that decryptedConfig is not empty
	decryptedConfig := dataBuffer.String()
	if decryptedConfig == "" {
		return "", errors.New("decrypted config is empty")
	}

	return decryptedConfig, err
}

// tossKey is responsible for changing up the byteKey.
func tossKey() []byte {
	// Add your cool byte array manipulations here!
	return randomBytes
}

// obfuscateData encodes the configuration when writing to ScoringData. This
// also makes exposure of sensitive memory less likely, since there is a smaller
// window of opportunity for catching plaintext data.
func obfuscateData(datum *string) error {
	var err error
	if *datum == "" {
		return nil
	}
	if *datum, err = encryptConfig(*datum); err == nil {
		*datum = hexEncode(xor(string(tossKey()), *datum))
	} else {
		fail("crypto: failed to obufscate datum: " + err.Error())
		return err
	}
	return nil
}

// deobfuscateData decodes configuration data.
func deobfuscateData(datum *string) error {
	var err error
	if *datum == "" {
		// empty data given to deobfuscateData-- not really a concern often this
		// is just empty/optional struct fields
		return nil
	}
	*datum, err = hexDecode(*datum)
	if err != nil {
		fail("crypto: failed to deobfuscate datum hex: " + err.Error())
		return err
	}
	*datum = xor(string(tossKey()), *datum)
	if *datum, err = decryptConfig(*datum); err != nil {
		fail("crypto: failed to deobfuscate datum: ", *datum, err.Error())
		return err
	}
	return nil
}

// encryptString takes a password and a plaintext and returns an encrypted byte
// sequence (as a string). It uses AES-GCM with a 12-byte IV (as is
// recommended). The IV is prefixed to the string.
func encryptString(password, plainText string) string {
	// Create a sha256sum hash of the password provided.
	hasher := sha256.New()
	_, err := hasher.Write([]byte(password))
	if err != nil {
		fail(err)
		return ""
	}
	key := hasher.Sum(nil)

	// Pad plainText to be a 16-byte block.
	paddingArray := make([]byte, (aes.BlockSize - len(plainText)%aes.BlockSize))
	for char := range paddingArray {
		paddingArray[char] = 0x20 // Padding with space character.
	}
	plainText = plainText + string(paddingArray)
	if len(plainText)%aes.BlockSize != 0 {
		fail("plainText is not a multiple of block size!")
		return ""
	}

	// Create cipher block with key.
	block, err := aes.NewCipher(key)
	if err != nil {
		fail(err)
		return ""
	}

	// Generate nonce.
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		fail(err)
		return ""
	}

	// Create NewGCM cipher.
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		fail(err)
		return ""
	}

	// Encrypt and seal plainText.
	ciphertext := aesgcm.Seal(nil, nonce, []byte(plainText), nil)
	ciphertext = []byte(fmt.Sprintf("%s%s", nonce, ciphertext))
	return string(ciphertext)
}

// decryptString takes a password and a ciphertext and returns a decrypted
// byte sequence (as a string). The function uses typical AES-GCM.
func decryptString(password, ciphertext string) string {
	// Create a sha256sum hash of the password provided.
	hasher := sha256.New()
	if _, err := hasher.Write([]byte(password)); err != nil {
		fail(err)
	}
	key := hasher.Sum(nil)

	// Grab the IV from the first 12 bytes of the file.
	iv := []byte(ciphertext[:12])
	ciphertext = ciphertext[12:]

	// Create the AES block object.
	block, err := aes.NewCipher(key)
	if err != nil {
		fail(err.Error())
		return ""
	}

	// Create the AES-GCM cipher with the generated block.
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		fail("Error creating AES cipher (please tell the developers):", err.Error())
		return ""
	}

	// Decrypt (and check validity, since it's GCM) of ciphertext.
	plainText, err := aesgcm.Open(nil, iv, []byte(ciphertext), nil)
	if err != nil {
		fail("Error decrypting (are you using the correct aeacus/phocus? you may need to re-encrypt your config):", err.Error())
		return ""
	}

	return strings.TrimSpace(string(plainText))
}