package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"golang.org/x/time/rate"

	sesV2Types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

type MailRequest struct {
	Subject string   `json:"subject"`
	Body    string   `json:"body"`
	To      []string `json:"to"`
	Cc      []string `json:"cc"`
	Bcc     []string `json:"bcc"`
}

type ExecutionConfig struct {
	Region                 string
	FromMail               string
	EmailRateInterval      time.Duration
	QueueURL               string
	MaxExecutionTime       time.Duration
	QueueVisibilityTimeout time.Duration
}

type CommunicationBus struct {
	Errors          chan error
	Messages        chan *types.Message
	ReadMessages    chan *types.Message
	MainWorkerGroup *sync.WaitGroup
	CancelFn        context.CancelFunc
}

func handler() error {

	execConfig, err := getExecutionConfigFromEnv()
	if err != nil {
		log.Printf("Error getting execution config from environment: %v", err)
		return err
	}

	ctx, cancelFn := context.WithTimeout(context.Background(), execConfig.MaxExecutionTime)
	defer cancelFn()

	config, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return err
	}

	config.Region = execConfig.Region
	sqsClient := sqs.NewFromConfig(config)
	sesClient := sesv2.NewFromConfig(config)

	communicationBus := &CommunicationBus{
		Errors:          make(chan error, 32),
		Messages:        make(chan *types.Message, 32),
		ReadMessages:    make(chan *types.Message, 32),
		MainWorkerGroup: &sync.WaitGroup{},
		CancelFn:        cancelFn,
	}

	communicationBus.MainWorkerGroup.Add(4)

	defer func() {
		if r := recover(); r != nil {
			log.Printf("Recovered from panic: %v", r)
		}
		cancelFn()
	}()

	go startErrorListener(communicationBus)
	go startQueueMessagesCollector(ctx, sqsClient, execConfig, communicationBus)
	go startMailerHandler(ctx, sesClient, communicationBus, execConfig)
	go startReadMessagesReadHandler(ctx, sqsClient, communicationBus, execConfig)

	communicationBus.MainWorkerGroup.Wait()

	return nil
}

func startErrorListener(communicationBus *CommunicationBus) {
	go func() {
		defer communicationBus.MainWorkerGroup.Done()
		for err := range communicationBus.Errors {
			log.Printf("Error: %v", err)
		}
	}()
}

func getExecutionConfigFromEnv() (*ExecutionConfig, error) {

	var exists bool
	var region string
	var fromMail string
	var emailRateInterval time.Duration
	var queueURL string
	var maxExecutionTime time.Duration

	if region, exists = os.LookupEnv("MAIL_QUEUE_REGION"); !exists || region == "" {
		return nil, fmt.Errorf("environment variable MAIL_QUEUE_REGION is not set or empty")
	}

	if fromMail, exists = os.LookupEnv("MAIL_FROM_ADDRESS"); !exists || fromMail == "" {
		return nil, fmt.Errorf("environment variable MAIL_FROM_ADDRESS is not set or empty")
	}

	if mailsPerSecondStr, exists := os.LookupEnv("MAILS_PER_SECOND"); !exists || mailsPerSecondStr == "" {
		return nil, fmt.Errorf("environment variable MAILS_PER_SECOND is not set or empty")
	} else {

		mailsPerSecond, err := strconv.Atoi(mailsPerSecondStr)
		if err != nil || mailsPerSecond <= 0 {
			return nil, fmt.Errorf("invalid value for MAILS_PER_SECOND: %v", err)
		}

		emailRateInterval = time.Second / time.Duration(mailsPerSecond)
	}

	if maxExecutionTimeStr, exists := os.LookupEnv("FUNCTION_MAX_EXECUTION_TIME"); !exists || maxExecutionTimeStr == "" {
		return nil, fmt.Errorf("environment variable FUNCTION_MAX_EXECUTION_TIME is not set or empty")
	} else {
		var err error
		maxExecutionTime, err = time.ParseDuration(maxExecutionTimeStr)
		if err != nil || maxExecutionTime <= 0 {
			return nil, fmt.Errorf("invalid value for FUNCTION_MAX_EXECUTION_TIME: %v", err)
		}
	}

	if queueURL, exists = os.LookupEnv("MAIL_QUEUE_URL"); !exists || queueURL == "" {
		return nil, fmt.Errorf("environment variable MAIL_QUEUE_URL is not set or empty")
	}

	return &ExecutionConfig{
		Region:                 region,
		FromMail:               fromMail,
		EmailRateInterval:      emailRateInterval,
		QueueURL:               queueURL,
		MaxExecutionTime:       maxExecutionTime,
		QueueVisibilityTimeout: maxExecutionTime + 5,
	}, nil
}

func startQueueMessagesCollector(ctx context.Context, sqsClient *sqs.Client, execConfig *ExecutionConfig, communicationBus *CommunicationBus) {
	go func() {
		defer close(communicationBus.Messages)
		defer communicationBus.MainWorkerGroup.Done()

		for {

			select {
			case <-ctx.Done():
				log.Printf("Context done, stopping message retrieval: %v", ctx.Err())
				return
			default:
			}

			resp, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
				QueueUrl:            aws.String(execConfig.QueueURL),
				MaxNumberOfMessages: 10,
				VisibilityTimeout:   int32(execConfig.QueueVisibilityTimeout.Seconds()),
				WaitTimeSeconds:     5,
			})
			if err != nil {
				log.Println("Error receiving messages from SQS !!!!:", err)
				communicationBus.Errors <- fmt.Errorf("error receiving messages from SQS: %w", err)
				return
			}

			for _, msg := range resp.Messages {
				communicationBus.Messages <- &msg
			}

			if len(resp.Messages) == 0 {
				log.Printf("No messages received")
				return
			}
		}
	}()
}

func startReadMessagesReadHandler(ctx context.Context, sqsClient *sqs.Client, communicationBus *CommunicationBus, execConfig *ExecutionConfig) {
	go func() {
		defer communicationBus.MainWorkerGroup.Done()
		defer close(communicationBus.Errors)
		for readMessage := range communicationBus.ReadMessages {
			_, err := sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
				QueueUrl:      aws.String(execConfig.QueueURL),
				ReceiptHandle: readMessage.ReceiptHandle,
			})
			if err != nil {
				communicationBus.Errors <- errors.New("error deleting message from SQS: " + err.Error())
			}
		}
	}()
}

func decodeBase64MessageBodyToMailRequest(messageBody string) (*MailRequest, error) {
	decodedData, err := base64.StdEncoding.DecodeString(messageBody)
	if err != nil {
		return nil, fmt.Errorf("error decoding base64 message body: %v", err)
	}

	var mailRequest MailRequest
	if err := json.Unmarshal(decodedData, &mailRequest); err != nil {
		return nil, fmt.Errorf("error unmarshalling decoded data to MailRequest: %v", err)
	}

	return &mailRequest, nil
}

func startMailerHandler(ctx context.Context, sesClient *sesv2.Client, communicationBus *CommunicationBus, execConfig *ExecutionConfig) {
	defer communicationBus.MainWorkerGroup.Done()
	defer close(communicationBus.ReadMessages)

	limiter := rate.NewLimiter(rate.Every(execConfig.EmailRateInterval), 1)
	waitGroup := &sync.WaitGroup{}

	for message := range communicationBus.Messages {
		select {
		case <-ctx.Done():
			communicationBus.Errors <- fmt.Errorf("context done, stopping mailer handler: %v", ctx.Err())
			return
		default:
		}

		err := limiter.Wait(ctx)
		if err != nil {
			communicationBus.Errors <- fmt.Errorf("rate limiter wait error: %w", err)
			break
		}

		waitGroup.Add(1)
		go sendMailRequestToSes(ctx, sesClient, waitGroup, communicationBus, message, execConfig)
	}

	waitGroup.Wait()
}

func sendMailRequestToSes(ctx context.Context, sesClient *sesv2.Client, waitgroup *sync.WaitGroup, communicationBus *CommunicationBus, message *types.Message, execConfig *ExecutionConfig) {
	defer waitgroup.Done()

	if message.Body == nil || *message.Body == "" {
		communicationBus.Errors <- fmt.Errorf("empty sqs message body: %s", *message.MessageId)
		communicationBus.ReadMessages <- message
		return
	}

	mailRequest, err := decodeBase64MessageBodyToMailRequest(*message.Body)
	if err != nil {
		communicationBus.Errors <- fmt.Errorf("failed to decode message body: %w, message ID: %s", err, *message.MessageId)
		communicationBus.ReadMessages <- message
		return
	}

	if mailRequest.Subject == "" && mailRequest.Body == "" {
		communicationBus.Errors <- fmt.Errorf("empty subject and body for mail request, message ID: %s", *message.MessageId)
		communicationBus.ReadMessages <- message
		return
	}

	if len(mailRequest.To) == 0 && len(mailRequest.Cc) == 0 && len(mailRequest.Bcc) == 0 {
		communicationBus.Errors <- fmt.Errorf("no recipients, message ID: %s", *message.MessageId)
		communicationBus.ReadMessages <- message
		return
	}

	input := &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(execConfig.FromMail),
		Destination: &sesV2Types.Destination{
			ToAddresses:  mailRequest.To,
			CcAddresses:  mailRequest.Cc,
			BccAddresses: mailRequest.Bcc,
		},
		Content: &sesV2Types.EmailContent{
			Simple: &sesV2Types.Message{
				Subject: &sesV2Types.Content{
					Data: aws.String(mailRequest.Subject),
				},
				Body: &sesV2Types.Body{
					Html: &sesV2Types.Content{
						Data: aws.String(mailRequest.Body),
					},
				},
			},
		},
	}

	_, err = sesClient.SendEmail(ctx, input)
	if err != nil {
		communicationBus.Errors <- fmt.Errorf("error sending email via SES: %w, message ID: %s", err, *message.MessageId)
		return
	}

	communicationBus.ReadMessages <- message
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

	if _, e := os.LookupEnv("AWS_LAMBDA_RUNTIME_API"); e {
		lambda.Start(handler)
	} else {
		if err := handler(); err != nil {
			log.Fatalf("Handler error: %v", err)
		}
	}
}
