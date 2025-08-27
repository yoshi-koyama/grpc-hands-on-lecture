package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	quizpb "github.com/yoshi-koyama/quiz/quizapp/gen"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	// grpc.NewClient が推奨（Dial 非推奨）。ローカル直指定なので passthrough を明示。
	conn, err := grpc.NewClient(
		"localhost:8080",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	c := quizpb.NewQuizClient(conn)

	ctx := context.Background()
	stream, err := c.Play(ctx)
	if err != nil {
		log.Fatal(err)
	}

	// 名前入力して Join
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("あなたの名前: ")
	name, _ := reader.ReadString('\n')

	name = strings.TrimSpace(name)
	if err := stream.Send(&quizpb.ClientMsg{
		Payload: &quizpb.ClientMsg_Join{Join: &quizpb.Join{Name: name}},
	}); err != nil {
		log.Fatal(err)
	}

	// 受信 goroutine
	go func() {
		for {
			in, err := stream.Recv()
			if err != nil {
				log.Println("recv err:", err)
				return
			}
			switch p := in.Payload.(type) {
			case *quizpb.ServerMsg_Welcome:
				fmt.Printf("[WELCOME] id=%s name=%s\n", p.Welcome.GetPlayerId(), p.Welcome.GetName())
			case *quizpb.ServerMsg_Info:
				fmt.Println("[INFO]", p.Info.GetText())
			case *quizpb.ServerMsg_Question:
				fmt.Printf("\n[Q%d] %s\n", p.Question.GetIndex(), p.Question.GetText())
				fmt.Println("  -> 早押しは 'b'、回答は 'a your_answer'")
			case *quizpb.ServerMsg_Buzz:
				fmt.Printf("[BUZZ] %s が回答権を獲得\n", p.Buzz.GetName())
			case *quizpb.ServerMsg_Judge:
				if p.Judge.GetCorrect() {
					fmt.Printf("[JUDGE] 正解！（%s）\n", p.Judge.GetComment())
				} else {
					fmt.Printf("[JUDGE] 不正解…（%s）\n", p.Judge.GetComment())
				}
			case *quizpb.ServerMsg_Board:
				fmt.Println("[SCORES]")
				for _, s := range p.Board.GetScores() {
					fmt.Printf("  %s : %d\n", s.GetName(), s.GetPoints())
				}
			case *quizpb.ServerMsg_Err:
				fmt.Println("[ERROR]", p.Err.GetText())
			}
		}
	}()

	// 入力ループ
	for {
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "q" || line == "quit" || line == "exit" {
			stream.CloseSend()
			fmt.Println("bye.")
			return
		}
		if line == "b" {
			_ = stream.Send(&quizpb.ClientMsg{
				Payload: &quizpb.ClientMsg_Buzz{Buzz: &quizpb.Buzz{TsMs: time.Now().UnixMilli()}},
			})
			continue
		}
		if strings.HasPrefix(line, "a ") {
			ans := strings.TrimSpace(strings.TrimPrefix(line, "a "))
			_ = stream.Send(&quizpb.ClientMsg{
				Payload: &quizpb.ClientMsg_Answer{Answer: &quizpb.Answer{Text: ans}},
			})
			continue
		}
		fmt.Println("コマンド: 早押しは 'b'、回答は 'a your_answer' q で終了")
	}
}
