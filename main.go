package main

import (
	"bufio"
	"context"
	"fmt"
	"math/rand/v2"
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

	Count int `arg:"" help:"Number of log groups to create"`
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

	for {
		select {
		case <-stopCh:
			fmt.Println("Stopping...")
			return nil
		case <-shutdownCh:
			return nil
		default:
			time.Sleep(randomDuration())
			lg := randomLogGroup(logGroups)
			err := cli.logMessage(lg, uuid.NewString())
			if err != nil {
				return fmt.Errorf("logging message: %w", err)
			}
		}
	}
}

func (cli *CLI) createLogGroups(numOfLogGroups int) ([]string, error) {
	var names []string
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

		names = append(names, name)
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

func (cli *CLI) deleteLogGroups(names []string) {
	fmt.Println("Cleaning up log groups...")
	for _, logGroupName := range names {
		_, err := cli.cw.DeleteLogGroup(context.Background(), &cloudwatchlogs.DeleteLogGroupInput{
			LogGroupName: aws.String(logGroupName),
		})
		if err != nil {
			fmt.Printf("ERROR could not delete log group %s: %s", logGroupName, err)
		}
	}
}

func randomLogGroup(logGroups []string) string {
	i := rand.IntN(len(logGroups))
	return logGroups[i]
}

func (cli *CLI) logMessage(logGroup, msg string) error {
	now := time.Now()
	fmt.Printf("%s [%s] %s\n", now.Format(time.RFC3339), logGroup, msg)
	_, err := cli.cw.PutLogEvents(context.Background(), &cloudwatchlogs.PutLogEventsInput{
		LogEvents: []types.InputLogEvent{
			{
				Message:   aws.String(msg),
				Timestamp: aws.Int64(now.UnixMilli()),
			},
		},
		LogGroupName:  aws.String(logGroup),
		LogStreamName: aws.String("DEFAULT"),
	})
	if err != nil {
		return fmt.Errorf("putting log events: %w", err)
	}

	return nil
}

// randomDuration returns a random duration between 0.5 and 1.0 second
func randomDuration() time.Duration {
	minD := 0.5
	maxD := 1.0

	randomFloat := minD + rand.Float64()*(maxD-minD)
	return time.Duration(randomFloat * float64(time.Second))
}
