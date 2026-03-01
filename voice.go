package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"

	"github.com/tectiv3/chatgpt-bot/opus"
	tele "gopkg.in/telebot.v3"
)

func convertToWav(r io.Reader) ([]byte, error) {
	output := new(bytes.Buffer)
	wavWriter, err := newWavWriter(output, 48000, 1, 16)
	if err != nil {
		return nil, err
	}

	s, err := opus.NewStream(r)
	if err != nil {
		return nil, err
	}
	defer s.Close()

	pcmbuf := make([]float32, 16384)
	for {
		n, err := s.ReadFloat32(pcmbuf)
		if err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		pcm := pcmbuf[:n*1]

		err = wavWriter.WriteSamples(pcm)
		if err != nil {
			return nil, err
		}
	}

	// Patch WAV header sizes now that we know the total data length
	buf := output.Bytes()
	dataSize := uint32(len(buf) - 44) // 44 = WAV header size
	binary.LittleEndian.PutUint32(buf[4:8], dataSize+36)
	binary.LittleEndian.PutUint32(buf[40:44], dataSize)

	return buf, nil
}

func newWavWriter(w io.Writer, sampleRate int, numChannels int, bitsPerSample int) (*wavWriter, error) {
	var header wavHeader

	header.RIFFID = [4]byte{'R', 'I', 'F', 'F'}
	header.WAVEID = [4]byte{'W', 'A', 'V', 'E'}
	header.FMTID = [4]byte{'f', 'm', 't', ' '}
	header.Subchunk1Size = 16
	header.AudioFormat = 1
	header.NumChannels = uint16(numChannels)
	header.SampleRate = uint32(sampleRate)
	header.BitsPerSample = uint16(bitsPerSample)
	header.ByteRate = uint32(sampleRate * numChannels * bitsPerSample / 8)
	header.BlockAlign = uint16(numChannels * bitsPerSample / 8)
	header.DataID = [4]byte{'d', 'a', 't', 'a'}

	err := binary.Write(w, binary.LittleEndian, &header)
	if err != nil {
		return nil, err
	}

	return &wavWriter{w: w}, nil
}

func (ww *wavWriter) WriteSamples(samples []float32) error {
	int16Samples := make([]int16, len(samples))
	for i, s := range samples {
		if s > 1.0 {
			s = 1.0
		} else if s < -1.0 {
			s = -1.0
		}
		int16Samples[i] = int16(s * 32767)
	}
	return binary.Write(ww.w, binary.LittleEndian, &int16Samples)
}

func (s *Server) handleVoice(c tele.Context) {
	if c.Message().Voice.FileSize == 0 {
		return
	}
	audioFile := c.Message().Voice.File
	var reader io.ReadCloser
	var err error

	if s.conf.TelegramServerURL != "" {
		f, err := c.Bot().FileByID(audioFile.FileID)
		if err != nil {
			Log.Warn("Error getting file ID", "error=", err)
			_ = c.Send("Voice error: failed to get file ID")
			return
		}
		reader, err = os.Open(f.FilePath)
		if err != nil {
			Log.Warn("Error opening file", "error=", err)
			_ = c.Send("Voice error: failed to open audio file")
			return
		}
	} else {
		reader, err = c.Bot().File(&audioFile)
		if err != nil {
			Log.Warn("Error getting file content", "error=", err)
			_ = c.Send("Voice error: failed to download audio")
			return
		}
	}
	defer reader.Close()

	wav, err := convertToWav(reader)
	if err != nil {
		Log.Warn("failed to convert to wav", "error=", err)
		_ = c.Send("Voice error: failed to convert audio")
		return
	}

	transcript, err := s.transcribe(wav)
	if err != nil {
		Log.Warn("failed to transcribe", "error=", err)
		_ = c.Send("Voice error: transcription failed")
		return
	}

	if strings.HasPrefix(strings.ToLower(transcript), "reset") {
		chat := s.getChat(c.Chat(), c.Sender())
		s.deleteHistory(chat.ID)
		return
	}

	s.complete(c, transcript, false)
}

// transcribe sends WAV audio to the configured whisper endpoint
func (s *Server) transcribe(wav []byte) (string, error) {
	if s.conf.WhisperEndpoint == "" {
		return "", fmt.Errorf("whisper_endpoint not configured")
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", fmt.Errorf("failed to create form file: %w", err)
	}
	if _, err := part.Write(wav); err != nil {
		return "", fmt.Errorf("failed to write audio: %w", err)
	}
	writer.Close()

	resp, err := http.Post(s.conf.WhisperEndpoint, writer.FormDataContentType(), &body)
	if err != nil {
		return "", fmt.Errorf("whisper request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("whisper returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Text string `json:"text"`
	}
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		// Fallback: treat as plain text
		return strings.TrimSpace(string(respBody)), nil
	}

	return result.Text, nil
}
