package main

import (
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

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

type MailRequest struct {
	Subject string   `json:"subject"`
	Body    string   `json:"body"`
	To      []string `json:"to"`
	Cc      []string `json:"cc"`
	Bcc     []string `json:"bcc"`
}

func validateMailRequest(mr *MailRequest) error {
	if mr.Subject == "" || mr.Body == "" || len(mr.To) == 0 || len(mr.Cc) == 0 || len(mr.Bcc) == 0 {
		return errors.New("all MailRequest fields must be filled")
	}
	return nil
}

func handler(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {

	var body []byte
	var err error
	var payload MailRequest

	body, err = getBody(request)
	if err != nil {
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusInternalServerError,
			Body:       http.StatusText(http.StatusInternalServerError),
		}, nil
	}

	err = json.Unmarshal(body, &payload)
	if err != nil {
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusInternalServerError,
			Body:       http.StatusText(http.StatusInternalServerError),
		}, nil
	}
	body = nil

	if err = validatePayload(&payload); err != nil {
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusBadRequest,
			Body:       err.Error(),
		}, nil
	}

	sqsClient := sqs.NewFromConfig(aws.Config{})
	queueURL := os.Getenv("MAIL_QUEUE_URL")

	_, err = sqsClient.SendMessage(context.Background(), &sqs.SendMessageInput{
		QueueUrl:    aws.String(queueURL),
		MessageBody: aws.String(string(body)),
	})
	if err != nil {
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusInternalServerError,
			Body:       http.StatusText(http.StatusInternalServerError),
		}, nil
	}

	return events.APIGatewayProxyResponse{
		StatusCode: 200,
		Body:       "Mail received and validated",
	}, nil
}

func getPayload(request events.APIGatewayProxyRequest) (*MailRequest, error) {

}

func validatePayload(payload *MailRequest) error {
	if payload == nil {
		return errors.New(http.StatusText(http.StatusBadRequest))
	}

	if payload.Subject == "" {
		return errors.New("subject is required")
	} else if payload.Body == "" {
		return errors.New("body is required")
	} else if len(payload.To) == 0 && len(payload.Cc) == 0 && len(payload.Bcc) == 0 {
		return errors.New("at least one recipient (to, cc, bcc) is required")
	}

	return nil
}

func getBody(request events.APIGatewayProxyRequest) ([]byte, error) {

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

func main() {

	if os.Getenv("FROM_EMAIL") == "" {
		log.Fatal("FROM_EMAIL environment variable is not set")
	}

	lambda.Start(handler)
}
