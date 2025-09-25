package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
		return errors.New("all MailRequest fields must be filled")
	}
	return nil
}

func handler(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {

	var err error
	var payload *MailRequest

	payload, err = getPayload(request)
	if err != nil {
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusInternalServerError,
			Body:       err.Error(),
		}, nil
	}

	if err = validateMailRequest(payload); err != nil {
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusBadRequest,
			Body:       err.Error(),
		}, nil
	}

	config, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusInternalServerError,
			Body:       err.Error(),
		}, nil
	}

	sqsClient := sqs.NewFromConfig(config)
	queueURL := os.Getenv("MAIL_QUEUE_URL")

	messageId, err := enqueueMailMessage(ctx, sqsClient, queueURL, payload)
	if err != nil {
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusInternalServerError,
			Body:       err.Error(),
		}, nil
	}

	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       "Message enqueued with ID: " + *messageId,
	}, nil
}

func enqueueMailMessage(ctx context.Context, sqsClient *sqs.Client, queueURL string, mailRequest *MailRequest) (*string, error) {

	jsonData, err := json.Marshal(mailRequest)
	if err != nil {
		return nil, err
	}

	base64Data := base64.StdEncoding.EncodeToString(jsonData)
	jsonData = nil

	out, err := sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(queueURL),
		MessageBody: aws.String(base64Data),
	})

	if err != nil {
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
		return nil, err
	}

	err = json.Unmarshal(body, &payload)
	if err != nil {
		return nil, err
	}
	body = nil

	return &payload, nil
}

func getBodyBytes(request events.APIGatewayProxyRequest) ([]byte, error) {

	var data []byte
	var err error

	if request.IsBase64Encoded {
		data, err = base64.StdEncoding.DecodeString(request.Body)
		if err != nil {
			return nil, err
		}
	}

	if content, isPresent := request.Headers[""]; isPresent && content == "gzip" {

		if data == nil {
			data = []byte(request.Body)
		}

		gzipReader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer gzipReader.Close()

		data, err = io.ReadAll(gzipReader)
		if err != nil {
			return nil, err
		}
	}

	return data, err
}

func readEnvFile() {
	file, err := os.Open(".env")
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		lineSplit := strings.SplitAfterN(line, "=", 2)
		key := strings.TrimSpace(lineSplit[0])
		value := strings.TrimSpace(lineSplit[1])

		if err := os.Setenv(key, value); err != nil {
			log.Fatal(err)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}
}

func main() {

	readEnvFile()

	lambda.Start(handler)
}
