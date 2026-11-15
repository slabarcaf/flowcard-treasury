// Command demo prepares fixtures, serves signed shortfall reads and evaluates through the API.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
	"unicode/utf8"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: demo prepare|serve-shortfalls|evaluate")
	}
	switch args[0] {
	case "prepare":
		return runPrepare(args[1:])
	case "serve-shortfalls":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return runServeShortfalls(ctx, args[1:])
	case "evaluate":
		return runEvaluate(args[1:])
	default:
		return errors.New("unknown demo subcommand")
	}
}

func decodeStrictJSON(data []byte, target any) error {
	if !utf8.Valid(data) || !validJSONObjects(data) {
		return errors.New("invalid_json")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("invalid_json")
	}
	return nil
}

func validJSONObjects(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if !scanJSONValue(decoder, 0) {
		return false
	}
	_, err := decoder.Token()
	return errors.Is(err, io.EOF)
}

func scanJSONValue(decoder *json.Decoder, depth int) bool {
	if depth > 64 {
		return false
	}
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return true
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return false
			}
			key, ok := keyToken.(string)
			if !ok {
				return false
			}
			if _, exists := seen[key]; exists {
				return false
			}
			seen[key] = struct{}{}
			if !scanJSONValue(decoder, depth+1) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim('}')
	case '[':
		count := 0
		for decoder.More() {
			count++
			if count > 10000 || !scanJSONValue(decoder, depth+1) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim(']')
	default:
		return false
	}
}

func readLimitedFile(path string, maximum int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("demo artifact unavailable")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(data)) > maximum {
		return nil, errors.New("demo artifact unavailable")
	}
	return data, nil
}

func writeJSONOutput(value any) error {
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		return errors.New("demo output unavailable")
	}
	return nil
}

func currentUTC() time.Time { return time.Now().UTC() }
