# AWS Serverless Golang Mail Service

Este projeto é um serviço de envio de e-mails construído com AWS Lambda, SQS e Go, utilizando arquitetura serverless e infraestrutura como código via AWS SAM.

## Visão Geral

O serviço é composto por dois microserviços principais:

- **receive-mail**: expõe uma API REST para receber requisições de envio de e-mail, valida os dados e envia a mensagem para uma fila SQS.
- **process-mail-queue**: consome mensagens da fila SQS e processa o envio dos e-mails.

A infraestrutura é definida no arquivo `template.yaml` utilizando AWS SAM, incluindo recursos como Lambda Functions, API Gateway e SQS.

## Estrutura do Projeto

```
├── process-mail-queue/ (adicionado futuramente)
├── receive-mail/
│   ├── main.go
│   ├── go.mod
│   ├── Dockerfile
│   └── .env
├── template.yaml
└── README.md
```

## Como Executar Localmente

### Pré-requisitos
- Docker
- AWS CLI configurado
- AWS SAM CLI
- Go 1.22+

### Passos

1. Clone o repositório e acesse a pasta do projeto.
2. Configure as variáveis de ambiente no arquivo `.env` dentro de `receive-mail`:

```env
# AWS Lambda environment variables
AWS_ACCESS_KEY_ID=ACCESS_KEY
AWS_SECRET_ACCESS_KEY=SECRET_KEY
MAIL_QUEUE_REGION=aw-region-00
MAIL_QUEUE_URL=https://sqs.re-gion-00.amazonaws.com/account-id/queue-name
DEBUG=(true|false)
```

5. Para testar a API localmente com o SAM:

```bash
sam build
sam local start-api ou sam local invoke ReceiveMailFunction -e ./receive-mail/event.json
```

Acesse: http://localhost:3000/mail

## Variáveis de Ambiente (`.env`)

```
AWS_ACCESS_KEY_ID=SEU_ACCESS_KEY
AWS_SECRET_ACCESS_KEY=SEU_SECRET_KEY
MAIL_QUEUE_REGION=sa-east-1
MAIL_QUEUE_URL=https://sqs.sa-east-1.amazonaws.com/012044602090/MailerQueue
DEBUG=true
```

- `AWS_ACCESS_KEY_ID` e `AWS_SECRET_ACCESS_KEY`: credenciais AWS.
- `MAIL_QUEUE_REGION`: região da fila SQS.
- `MAIL_QUEUE_URL`: URL da fila SQS.
- `DEBUG`: ativa logs detalhados.

## Deploy na AWS

Utilize o AWS SAM para empacotar e fazer o deploy:

```bash
sam build
sam deploy --guided
```

---

Contribuições são bem-vindas!
