package main

import (
	"bytes"
	"encoding/binary"
	"github.com/meinside/openai-go"
	"github.com/sunicy/go-lame"
	"github.com/tectiv3/chatgpt-bot/opus"
	tele "gopkg.in/telebot.v3"
	"io"
	"log"
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
			log.Fatal(err)
		}
		pcm := pcmbuf[:n*1]

		err = wavWriter.WriteSamples(pcm)
		if err != nil {
			return nil, err
		}
	}

	return output.Bytes(), err
}

// Helper function to create a new WAV writer
func newWavWriter(w io.Writer, sampleRate int, numChannels int, bitsPerSample int) (*wavWriter, error) {
	var header wavHeader

	// Set header values
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

	// Write header
	err := binary.Write(w, binary.LittleEndian, &header)
	if err != nil {
		return nil, err
	}

	return &wavWriter{w: w}, nil
}

// WriteSamples Write samples to the WAV file
func (ww *wavWriter) WriteSamples(samples []float32) error {
	// Convert float32 samples to int16 samples
	int16Samples := make([]int16, len(samples))
	for i, s := range samples {
		if s > 1.0 {
			s = 1.0
		} else if s < -1.0 {
			s = -1.0
		}
		int16Samples[i] = int16(s * 32767)
	}
	// Write int16 samples to the WAV file
	return binary.Write(ww.w, binary.LittleEndian, &int16Samples)
}

func wavToMp3(wav []byte) []byte {
	reader := bytes.NewReader(wav)
	wavHdr, err := lame.ReadWavHeader(reader)
	if err != nil {
		log.Println("not a wav file, err=" + err.Error())
		return nil
	}
	output := new(bytes.Buffer)
	wr, _ := lame.NewWriter(output)
	defer wr.Close()

	wr.EncodeOptions = wavHdr.ToEncodeOptions()
	if _, err := io.Copy(wr, reader); err != nil {
		return nil
	}

	return output.Bytes()
}

func (s Server) handleVoice(c tele.Context) {
	if c.Message().Voice.FileSize == 0 {
		return
	}
	audioFile := c.Message().Voice.File
	log.Println("Audio file: ", audioFile.FilePath, audioFile.FileSize, audioFile.FileID, audioFile.FileURL)

	reader, err := c.Bot().File(&audioFile)
	if err != nil {
		log.Println("Error getting file content:", err)
		return
	}
	defer reader.Close()

	//body, err := ioutil.ReadAll(reader)
	//if err != nil {
	//	fmt.Println("Error reading file content:", err)
	//	return nil
	//}

	wav, err := convertToWav(reader)
	if err != nil {
		log.Println("failed to convert to wav: ", err)
		return
	}
	mp3 := wavToMp3(wav)
	if mp3 == nil {
		log.Println("failed to convert to mp3")
		return
	}
	audio := openai.NewFileParamFromBytes(mp3)
	transcript, err := s.ai.CreateTranscription(audio, "whisper-1", nil)
	if err != nil {
		log.Printf("failed to create transcription: %s\n", err)
		return
	}
	if transcript.JSON == nil &&
		transcript.Text == nil &&
		transcript.SRT == nil &&
		transcript.VerboseJSON == nil &&
		transcript.VTT == nil {
		log.Println("There was no returned data")

		return
	}

	s.complete(c, *transcript.Text, false)

	return
}
