package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"os/signal"
	"time"

	"github.com/alecthomas/kong"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/google/uuid"
)

type CLI struct {
	cw *cloudwatchlogs.Client

	Count    int           `arg:"" help:"Number of log groups to create"`
	Duration time.Duration `name:"duration" default:"10m" help:"How long to send log messages"`
}

type logDetails struct {
	name  string
	seqId int
}

func main() {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		panic(err)
	}

	kongCtx := kong.Parse(&CLI{
		cw: cloudwatchlogs.NewFromConfig(cfg),
	})
	err = kongCtx.Run()
	kongCtx.FatalIfErrorf(err)
	return

}

func (cli *CLI) Run() error {
	logGroups, err := cli.createLogGroups(cli.Count)
	defer cli.deleteLogGroups(logGroups)

	if err != nil {
		return fmt.Errorf("creating log groups: %w", err)
	}

	var shutdownCh = make(chan struct{})
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		close(shutdownCh)
	}()

	stopCh := make(chan struct{})
	go func() {
		fmt.Printf("Created %d log groups\n", len(logGroups))
		fmt.Println("Started logging - press enter to stop...")
		_, err = bufio.NewReader(os.Stdin).ReadBytes('\n')
		if err != nil {
			panic(err)
		}

		close(stopCh)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), cli.Duration)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("Provided duration (%s) elapsed, stopping...\n", cli.Duration)
			return nil
		case <-stopCh:
			fmt.Println("Stopping...")
			return nil
		case <-shutdownCh:
			return nil
		case <-time.After(randomDuration()):
			i, err := rand.Int(rand.Reader, big.NewInt(int64(len(logGroups))))
			if err != nil {
				return fmt.Errorf("getting random index for log groups: %w", err)
			}

			det := logGroups[i.Int64()]
			det.seqId++
			err = cli.logMessage(det, uuid.NewString())
			if err != nil {
				return fmt.Errorf("logging message: %w", err)
			}
		}
	}
}

func (cli *CLI) createLogGroups(numOfLogGroups int) ([]*logDetails, error) {
	var names []*logDetails
	for i := range numOfLogGroups {
		name := fmt.Sprintf("log-group-%d", i+1)
		_, err := cli.cw.CreateLogGroup(context.Background(), &cloudwatchlogs.CreateLogGroupInput{
			LogGroupName:              aws.String(name),
			DeletionProtectionEnabled: nil,
			KmsKeyId:                  nil,
			LogGroupClass:             "",
			Tags:                      nil,
		})
		if err != nil {
			return names, fmt.Errorf("could not create log group %s: %v", name, err)
		}

		names = append(names, &logDetails{name: name})
		_, err = cli.cw.CreateLogStream(context.Background(), &cloudwatchlogs.CreateLogStreamInput{
			LogGroupName:  aws.String(name),
			LogStreamName: aws.String("DEFAULT"),
		})
		if err != nil {
			return names, fmt.Errorf("could not create log stream for log group %s: %v", name, err)
		}
	}

	return names, nil
}

func (cli *CLI) deleteLogGroups(details []*logDetails) {
	fmt.Println("Cleaning up log groups...")
	for _, det := range details {
		_, err := cli.cw.DeleteLogGroup(context.Background(), &cloudwatchlogs.DeleteLogGroupInput{
			LogGroupName: aws.String(det.name),
		})
		if err != nil {
			fmt.Printf("ERROR could not delete log group %s: %s", det.name, err)
		}
	}
}

func (cli *CLI) logMessage(det *logDetails, msg string) error {
	now := time.Now()
	fmt.Printf("%s [%s] message-%d %s\n", now.Format(time.RFC3339), det.name, det.seqId, msg)
	_, err := cli.cw.PutLogEvents(context.Background(), &cloudwatchlogs.PutLogEventsInput{
		LogEvents: []types.InputLogEvent{
			{
				Message:   aws.String(msg),
				Timestamp: aws.Int64(now.UnixMilli()),
			},
		},
		LogGroupName:  aws.String(det.name),
		LogStreamName: aws.String("DEFAULT"),
	})
	if err != nil {
		return fmt.Errorf("putting log events: %w", err)
	}

	return nil
}

// randomDuration returns a random duration between 0.5 and 1.0 second
func randomDuration() time.Duration {
	const precision = 1_000_000
	n, err := rand.Int(rand.Reader, big.NewInt(precision))
	if err != nil {
		panic(err)
	}

	randomFloat := 0.5 + float64(n.Int64())/float64(precision)*0.5
	return time.Duration(randomFloat * float64(time.Second))
}
