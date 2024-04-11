package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"github.com/meinside/openai-go"
	"github.com/tectiv3/chatgpt-bot/opus"
	"github.com/tectiv3/go-lame"
	tele "gopkg.in/telebot.v3"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
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
			Log.Fatal(err)
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
		Log.Warn("not a wav file", "error=", err.Error())
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
			return
		}
		// start reader from f.FilePath
		reader, err = os.Open(f.FilePath)
		if err != nil {
			Log.Warn("Error opening file", "error=", err)
			return
		}
	} else {
		reader, err = c.Bot().File(&audioFile)
		if err != nil {
			Log.Warn("Error getting file content", "error=", err)
			return
		}
	}
	defer reader.Close()

	//body, err := ioutil.ReadAll(reader)
	//if err != nil {
	//	fmt.Println("Error reading file content:", err)
	//	return nil
	//}

	wav, err := convertToWav(reader)
	if err != nil {
		Log.Warn("failed to convert to wav", "error=", err)
		return
	}
	mp3 := wavToMp3(wav)
	if mp3 == nil {
		Log.Warn("failed to convert to mp3")
		return
	}
	audio := openai.NewFileParamFromBytes(mp3)
	transcript, err := s.ai.CreateTranscription(audio, "whisper-1", nil)
	if err != nil {
		Log.Warn("failed to create transcription", "error=", err)
		return
	}
	if transcript.JSON == nil &&
		transcript.Text == nil &&
		transcript.SRT == nil &&
		transcript.VerboseJSON == nil &&
		transcript.VTT == nil {
		Log.Warn("There was no returned data")

		return
	}

	if strings.HasPrefix(strings.ToLower(*transcript.Text), "reset") {
		chat := s.getChat(c.Chat(), c.Sender())
		s.deleteHistory(chat.ID)

		v := &tele.Voice{File: tele.FromDisk("erased.ogg")}
		_ = c.Send(v)

		return
	}

	response, err := s.answer(c, *transcript.Text, nil)

	Log.Infof("User: %s. Response length: %d\n", c.Sender().Username, len(response))

	if len(response) == 0 {
		return
	}

	s.sendAudio(c, response)

	return
}

func (s *Server) sendAudio(c tele.Context, text string) {
	url := "https://api.openai.com/v1/audio/speech"
	body := map[string]string{
		"model":           "tts-1",
		"input":           text,
		"voice":           "alloy",
		"response_format": "opus",
		"speed":           "1",
	}
	jsonStr, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
	req.Header.Set("Authorization", "Bearer "+s.conf.OpenAIAPIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		Log.Warn("failed to send request", "error=", err)
	}
	defer resp.Body.Close()

	out, err := os.CreateTemp("", "chatbot")
	if err != nil {
		Log.Warn("failed to create temp file", "error=", err)
		return
	}

	_, err = io.Copy(out, resp.Body)
	if err := out.Close(); err != nil {
		return
	}

	v := &tele.Voice{File: tele.FromDisk(out.Name())}
	defer os.Remove(out.Name())
	_ = c.Send(v)
}

func (s *Server) textToSpeech(c tele.Context, text, lang string) error {
	switch lang {
	case "en":
	case "fr":
	case "ru":
		break
	default:
		s.sendAudio(c, text)
		return nil
	}
	if len(s.conf.PiperDir) == 0 {
		return c.Send("PiperDir is not set")
	}
	cmd := exec.Command(s.conf.PiperDir+"piper", "-m", s.conf.PiperDir+lang+".onnx", "-f", "-")

	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	go io.Copy(os.Stderr, stderr)

	out, err := os.CreateTemp("", "piper.wav")
	if err != nil {
		return c.Send("Error creating temp file: " + err.Error())
	}
	defer out.Close()

	if err := cmd.Start(); err != nil {
		return c.Send("Error starting command: " + err.Error())
	}
	if _, err := stdin.Write([]byte(text)); err != nil {
		return c.Send("Error writing to command: " + err.Error())
	}
	stdin.Close()
	_, err = io.Copy(out, stdout)
	if err != nil {
		return c.Send("Error reading from the command: " + err.Error())
	}
	if err := cmd.Wait(); err != nil {
		return c.Send("Error waiting for command: " + err.Error())
	}

	Log.Info("TTS done", "file", out.Name())
	v := &tele.Voice{File: tele.FromDisk(out.Name())}
	defer os.Remove(out.Name())

	return c.Send(v)
}
