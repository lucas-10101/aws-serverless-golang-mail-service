package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

type MailRequest struct {
	Subject string   `json:"subject"`
	Body    string   `json:"body"`
	To      []string `json:"to"`
	Cc      []string `json:"cc"`
	Bcc     []string `json:"bcc"`
}

func validateMailRequest(mail *MailRequest) error {
	if mail.Subject == "" || mail.Body == "" || len(mail.To) == 0 || len(mail.Cc) == 0 || len(mail.Bcc) == 0 {
		log.Printf("Invalid MailRequest")
		return errors.New("all MailRequest fields must be filled")
	}
	return nil
}

func handler(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {

	var err error
	var payload *MailRequest

	payload, err = getPayload(request)
	if err != nil {
		log.Printf("Error getting payload: %v", err)
		return createJsonProxyResponse(http.StatusBadRequest, err.Error()), nil
	}

	if err = validateMailRequest(payload); err != nil {
		log.Printf("Invalid mail request: %v", err)
		return createJsonProxyResponse(http.StatusBadRequest, err.Error()), nil
	}

	config, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Printf("Error loading AWS config: %v", err)
		return createJsonProxyResponse(http.StatusInternalServerError, err.Error()), nil
	}

	config.Region = os.Getenv("MAIL_QUEUE_REGION")
	sqsClient := sqs.NewFromConfig(config)
	queueURL := os.Getenv("MAIL_QUEUE_URL")

	messageId, err := enqueueMailMessage(ctx, sqsClient, queueURL, payload)
	if err != nil {
		log.Printf("Error enqueuing mail message: %v", err)
		return createJsonProxyResponse(http.StatusInternalServerError, err.Error()), nil
	}

	return createJsonProxyResponse(http.StatusOK, fmt.Sprintf("Message enqueued with ID: %s", *messageId)), nil
}

func enqueueMailMessage(ctx context.Context, sqsClient *sqs.Client, queueURL string, mailRequest *MailRequest) (*string, error) {

	jsonData, err := json.Marshal(mailRequest)
	if err != nil {
		log.Printf("Error marshaling mail request to JSON: %v", err)
		return nil, err
	}

	base64Data := base64.StdEncoding.EncodeToString(jsonData)
	jsonData = nil

	out, err := sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(queueURL),
		MessageBody: aws.String(base64Data),
	})

	if err != nil {
		log.Printf("Error sending message to SQS: %v", err)
		return nil, err
	}

	return out.MessageId, nil
}

func getPayload(request events.APIGatewayProxyRequest) (*MailRequest, error) {

	var body []byte
	var err error
	var payload MailRequest

	body, err = getBodyBytes(request)
	if err != nil {
		log.Printf("Error getting body bytes: %v", err)
		return nil, err
	}

	err = json.Unmarshal(body, &payload)
	if err != nil {
		log.Printf("Error unmarshaling JSON body: %v", err)
		return nil, err
	}
	body = nil

	return &payload, nil
}

func getBodyBytes(request events.APIGatewayProxyRequest) ([]byte, error) {

	var data []byte
	var err error

	if request.Body == "" {
		log.Printf("Empty body in request")
		return nil, errors.New("empty body")
	}

	if request.IsBase64Encoded {
		data, err = base64.StdEncoding.DecodeString(request.Body)
		if err != nil {
			log.Printf("Error decoding base64 body: %v", err)
			return nil, err
		}
	}

	if content, isPresent := request.Headers["Content-Encoding"]; isPresent && content == "gzip" {

		log.Printf("Decompressing gzip body")

		if data == nil {
			data = []byte(request.Body)
		}

		gzipReader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			log.Printf("Error creating gzip reader: %v", err)
			return nil, err
		}
		defer gzipReader.Close()

		data, err = io.ReadAll(gzipReader)
		if err != nil {
			log.Printf("Error reading gzip body: %v", err)
			return nil, err
		}
	}

	if data == nil {
		data = []byte(request.Body)
	}

	return data, err
}

func readEnvFile() {
	file, err := os.Open(".env")
	if err != nil {
		log.Fatalf("Error opening .env file: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		lineSplit := strings.Split(line, "=")

		if len(lineSplit) != 2 {
			continue
		}

		key := strings.TrimSpace(lineSplit[0])
		value := strings.TrimSpace(lineSplit[1])

		if err := os.Setenv(key, value); err != nil {
			log.Fatalf("Error setting environment variable %q: %v", key, err)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatalf("Error reading .env file: %v", err)
	}
}

func createJsonProxyResponse(status int, body string) events.APIGatewayProxyResponse {

	jsonBody, err := json.Marshal(map[string]string{
		"message": body,
	})
	if err != nil {
		log.Printf("Error marshaling JSON response body: %v", err)
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusInternalServerError,
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
			Body: fmt.Sprintf(`{"message": "%s"}`, err.Error()),
		}
	}

	body = string(jsonBody)

	return events.APIGatewayProxyResponse{
		StatusCode: status,
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
		Body: body,
	}
}

func main() {

	readEnvFile()

	fmt.Println("DEBUG:", os.Getenv("DEBUG"))

	if os.Getenv("DEBUG") != "true" {
		descriptor, err := os.Open(os.DevNull)
		if err != nil {
			log.Println("Unable to open null device:", err)
		}
		log.SetOutput(descriptor)
	} else {
		log.SetOutput(os.Stdout)
	}

	lambda.Start(handler)
}
