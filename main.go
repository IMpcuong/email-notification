package main

import (
	"context"
	"database/sql"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ses"
	_ "github.com/go-sql-driver/mysql"
)

var rdsProp = map[string]string{
	"DB_USERNAME":   "user",
	"DB_PASSWORD":   "password",
	"DB_ENDPOINT":   "tcp(db-instance.db-id.ap-southeast-1.rds.amazonaws.com:3307)/schema",
	"DB_SECRET_ARN": "/secret/arn",
}

type TxnStat struct {
	Auth   sql.NullInt16 // Nullable `int16` datatype.
	Reject sql.NullInt16
	New    sql.NullInt16
}

func (ts TxnStat) Stringify() string {
	tsFields := reflect.TypeOf(ts)
	totalFields := tsFields.NumField()
	tsValues := reflect.ValueOf(ts)

	txnStrArr := make([]string, 0, totalFields)
	for idx := 0; idx < totalFields; idx++ {
		field := tsFields.Field(idx)
		value := tsValues.Field(idx)
		txnStrArr = append(txnStrArr, fmt.Sprintf("Transaction-Type %v: %v", field.Name, value))
	}

	return strings.Join(txnStrArr, "<br/>")
}

const (
	SQL_FILE = "txn_summarize.sql"
	SQL_STMT = `
select
	sum(if(txn_stat = 'AUTHEN', quantity, NULL)) as AUTHEN,
	sum(if(txn_stat = 'REJECT', quantity, NULL)) as REJECT,
	sum(if(txn_stat = 'NEW', quantity, NULL)) as NEW,
from (
	select count(*) as quantity, t.txn_status
	from txn_table t
	where
  	1 = 1
  	and t.last_modified >= timestamp(curdate()-1)
  	and t.last_modified < timestamp(curdate())
	group by
  	t.txn_status
) as T;
`
)

func handleErr(err error) {
	if err != nil {
		log.Print(err.Error())
	}
}

func pingDBAlive(db *sql.DB) {
	err := db.Ping()
	handleErr(err)
}

func connectDB(acc, passwd, endpoint string) *sql.DB {
	dbCred := fmt.Sprintf("%s:%s@%s", acc, passwd, endpoint)
	db, err := sql.Open("mysql", dbCred)
	db.SetMaxIdleConns(64)
	db.SetMaxOpenConns(64)
	db.SetConnMaxLifetime(time.Minute)
	handleErr(err)

	return db
}

func execQuery(query string, db *sql.DB) *TxnStat {
	rows, err := db.Query(query)
	handleErr(err)

	sumTxnStat := new(TxnStat)
	for rows.Next() {
		err = rows.Scan(&sumTxnStat.Auth, &sumTxnStat.Reject, &sumTxnStat.New)
		handleErr(err)
		fmt.Println(sumTxnStat)

		return sumTxnStat
	}

	return sumTxnStat
}

func invokeEnvVar(key string) string {
	if envVar, exist := os.LookupEnv(key); !exist {
		envVar = rdsProp[key]
		return envVar
	} else {
		return envVar
	}
}

const (
	Region  = "ap-southeast-1"
	Sender  = "your-email@gmail.com"
	Subject = "Daily report: Transaction summarization"
	CharSet = "UTF-8"
)

func createSESSess() *ses.SES {
	sess, err := session.NewSession(&aws.Config{
		Region: aws.String(Region),
	})
	handleErr(err)

	sesSvc := ses.New(sess)
	return sesSvc
}

func convertToAwsSlice(from []string) []*string {
	awsSlice := make([]*string, 0, len(from))
	for _, ele := range from {
		awsSlice = append(awsSlice, aws.String(ele))
	}
	return awsSlice
}

func genEmailSubject() *string {
	subject := fmt.Sprintf("%s %d-%s-%d", Subject, time.Now().Day(), time.Now().Month(), time.Now().Year())
	return aws.String(subject)
}

var (
	DefaultRecipients = []string{"your-email@gmail.com", "your-email1@gmail.com"}
	DefaultCfgSets    = []string{"Default-CfgSets"}
)

func getListRecipients(sesSvc *ses.SES, defaultList []string) []*string {
	listRecipient, err := sesSvc.ListIdentities(&ses.ListIdentitiesInput{
		IdentityType: aws.String("EmailAddress"),
	})
	handleErr(err)

	emails := listRecipient.Identities
	if len(emails) == 0 {
		emails = func(emailConsts []string) []*string {
			for _, r := range emailConsts {
				emails = append(emails, aws.String(r))
			}
			return emails
		}(defaultList)
	}

	return emails
}

func getListConfigSets(sesSvc *ses.SES, defaultList []string, maxItems int64) []*string {
	listConfigs, err := sesSvc.ListConfigurationSets(&ses.ListConfigurationSetsInput{
		MaxItems: aws.Int64(maxItems),
	})
	handleErr(err)

	sesCfgSets := make([]*string, 0, maxItems)
	retrievableCfgSets := listConfigs.ConfigurationSets
	for _, cfg := range retrievableCfgSets {
		sesCfgSets = append(sesCfgSets, cfg.Name)
		return sesCfgSets
	}

	if len(sesCfgSets) == 0 {
		sesCfgSets := convertToAwsSlice(defaultList)
		return sesCfgSets
	}

	return sesCfgSets
}

func genSESEmailTpl(body string, sesSvc *ses.SES) *ses.SendEmailInput {
	recepientsAwsStr := getListRecipients(sesSvc, DefaultRecipients)
	cfgSets := getListConfigSets(sesSvc, DefaultCfgSets, 1)

	emailTpl := &ses.SendEmailInput{
		Destination: &ses.Destination{
			CcAddresses: []*string{},
			ToAddresses: recepientsAwsStr,
		},
		Message: &ses.Message{
			Body: &ses.Body{
				Html: &ses.Content{
					Charset: aws.String(CharSet),
					Data:    aws.String("<h2>Amazon SES Service for daily transaction report (Using AWS-SDK)</h2>" + body),
				},
				Text: &ses.Content{
					Charset: aws.String(CharSet),
					Data:    aws.String(body), // NOTE: This field's value didn't rendered on Email-UI.
				},
			},
			Subject: &ses.Content{
				Charset: aws.String(CharSet),
				Data:    genEmailSubject(),
			},
		},
		Source:               aws.String(Sender),
		ConfigurationSetName: cfgSets[0],
	}

	return emailTpl
}

func handleSESErr(err error) {
	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok {
			switch awsErr.Code() {
			case ses.ErrCodeMessageRejected:
				fmt.Println(ses.ErrCodeMessageRejected, awsErr.Error())
			case ses.ErrCodeMailFromDomainNotVerifiedException:
				fmt.Println(ses.ErrCodeMailFromDomainNotVerifiedException, awsErr.Error())
			case ses.ErrCodeConfigurationSetDoesNotExistException:
				fmt.Println(ses.ErrCodeConfigurationSetDoesNotExistException, awsErr.Error())
			default:
				fmt.Println(awsErr.Error())
			}
		} else {
			// Print the error, cast err to awserr.Error to get the Code and
			// Message from an error.
			fmt.Println(err.Error())
		}

		return
	}
}

// NOTE:
// The default AWS credentials must be declared inside the "~/.aws/credentials" file
// or else this error will be appreared: failed to refresh cached credentials, no EC2 IMDS role found, operation error ec2imds: GetMetadata,
// exceeded maximum number of attempts, 3, connectex: A socket operation was attempted to an unreachable network.

type Trigger struct {
	Flag uint8 `json:"flag"`
}

func handleReq(ctx context.Context, trigger Trigger) (TxnStat, error) {
	if trigger.Flag != 0 {
		return *new(TxnStat), nil
	}

	deadline := 8 * time.Second
	duration := time.Now().Add(deadline)
	ctxWithDeadline, cancel := context.WithDeadline(ctx, duration)

	defer cancel()

	select {
	case <-time.After(10 * time.Second):
		log.Fatalln("Error: Deadline violation!")
	case <-ctxWithDeadline.Done():
		sqlStmt := getQueryFromFile(SQL_FILE)
		if sqlStmt == "" {
			sqlStmt = SQL_STMT
		}

		rdsAcc := invokeEnvVar("DB_USERNAME")
		rdsPass := invokeEnvVar("DB_PASSWORD")
		rdsEndpoint := invokeEnvVar("DB_ENDPOINT")
		db := connectDB(rdsAcc, rdsPass, rdsEndpoint)

		defer db.Close()

		pingDBAlive(db)
		txnStat := execQuery(sqlStmt, db)
		strTxnStat := txnStat.Stringify()

		sesSvc := createSESSess()
		sesEmailTpl := genSESEmailTpl(strTxnStat, sesSvc)
		emailRes, err := sesSvc.SendEmail(sesEmailTpl)
		handleSESErr(err)
		fmt.Println("Email sent to address: \n" + strings.Join(DefaultRecipients, "\n"))
		fmt.Println(emailRes.MessageId, emailRes.GoString())

		return *txnStat, nil
	}

	return *new(TxnStat), nil
}

func getQueryFromFile(fileName string) string {
	query, err := ioutil.ReadFile(fileName)
	if err != nil {
		fmt.Printf("Failed to reading data from file: %s\n", err.Error())
	}

	return string(query)
}

// NOTE:
// To avoid mutual functions in the same package are undefined, just run (with 2 options):
// 	+ `go run *.go`
// 	+ `go build . && go run main.go`
func main() {
	lambda.Start(handleReq)
}
