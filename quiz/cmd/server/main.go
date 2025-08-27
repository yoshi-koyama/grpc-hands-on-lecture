package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	quizpb "github.com/yoshi-koyama/quiz/quizapp/gen"
	"google.golang.org/grpc"
)

type player struct {
	id    string
	name  string
	sendC chan *quizpb.ServerMsg
}

type hub struct {
	mu       sync.Mutex
	players  map[string]*player
	scores   map[string]int
	joinC    chan *player
	leaveC   chan string
	evC      chan clientEvent
	qs       []qa
	qidx     int
	activeQ  bool
	answerer string // 早押し獲得者の player_id
}

type qa struct {
	q string
	a string
}

type clientEvent struct {
	from string
	msg  *quizpb.ClientMsg
}

func newHub() *hub {
	return &hub{
		players: make(map[string]*player),
		scores:  make(map[string]int),
		joinC:   make(chan *player),
		leaveC:  make(chan string),
		evC:     make(chan clientEvent, 128),
		qs: []qa{
			{"日本の首都は？", "東京都"},
			{"日本一広い湖は？", "琵琶湖"},
			{"日本で一番高い山は？", "富士山"},
		},
	}
}

func (h *hub) run() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// 最初の問題はプレイヤーが1人以上になったら出す
	for {
		select {
		// 参加処理の中
		case p := <-h.joinC:
			// 1) 登録と状態のスナップショットを取得（ロック内）
			h.mu.Lock()
			h.players[p.id] = p
			if _, ok := h.scores[p.name]; !ok {
				h.scores[p.name] = 0
			}
			active := h.activeQ
			var qText string
			var qIndex int32
			if active {
				qIndex = int32(h.qidx + 1)
				qText = h.qs[h.qidx].q
			}
			ansID := h.answerer
			ansName := ""
			if a := h.players[ansID]; a != nil {
				ansName = a.name
			}
			// スコアのコピーを作る
			scores := make([]*quizpb.Score, 0, len(h.scores))
			for n, sc := range h.scores {
				scores = append(scores, &quizpb.Score{Name: n, Points: int32(sc)})
			}
			h.mu.Unlock()

			// 2) 全員に参加通知
			h.broadcast(&quizpb.ServerMsg{Payload: &quizpb.ServerMsg_Info{
				Info: &quizpb.Info{Text: fmt.Sprintf("%s が参加しました", p.name)},
			}})

			// 3) 新参加者にだけ現在の状態を“個別送信”（ロック外）
			if active {
				p.sendC <- &quizpb.ServerMsg{Payload: &quizpb.ServerMsg_Question{
					Question: &quizpb.Question{Index: qIndex, Text: qText},
				}}
				if ansID != "" {
					p.sendC <- &quizpb.ServerMsg{Payload: &quizpb.ServerMsg_Buzz{
						Buzz: &quizpb.BuzzResult{PlayerId: ansID, Name: ansName},
					}}
				}
			}
			if len(scores) > 0 {
				p.sendC <- &quizpb.ServerMsg{Payload: &quizpb.ServerMsg_Board{
					Board: &quizpb.Scoreboard{Scores: scores},
				}}
			}

			// 4) もしまだ出題が始まっていないなら開始（既存どおり）
			if !active && len(h.players) >= 1 {
				h.nextQuestion()
			}

		case id := <-h.leaveC:
			h.mu.Lock()
			// delete score
			delete(h.scores, h.players[id].name)
			p := h.players[id]
			delete(h.players, id)
			h.mu.Unlock()
			if p != nil {
				h.broadcast(&quizpb.ServerMsg{Payload: &quizpb.ServerMsg_Info{Info: &quizpb.Info{
					Text: fmt.Sprintf("%s が退出しました", p.name),
				}}})
			}

		case ev := <-h.evC:
			log.Println("event from", ev.from)
			switch pl := ev.msg.Payload.(type) {
			case *quizpb.ClientMsg_Join:
				// 参加は最初にサーバ側で Welcome を送っているので特別処理は不要
				_ = pl
			case *quizpb.ClientMsg_Buzz:
				log.Println("buzz from", ev.from)
				h.onBuzz(ev.from)
			case *quizpb.ClientMsg_Answer:
				log.Println("answer from", ev.from, ":", pl.Answer.GetText())
				h.onAnswer(ev.from, pl.Answer.GetText())
			case *quizpb.ClientMsg_Hb:
				// no-op
			}

		case <-ticker.C:
			// 必要なら Keepalive/残り時間表示 等
		}
	}
}

func (h *hub) broadcast(msg *quizpb.ServerMsg) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, p := range h.players {
		select {
		case p.sendC <- msg:
		default:
		}
	}
}

func (h *hub) nextQuestion() {
	if h.qidx >= len(h.qs) {
		h.broadcast(&quizpb.ServerMsg{Payload: &quizpb.ServerMsg_Info{Info: &quizpb.Info{
			Text: "全問終了！お疲れさまでした。",
		}}})
		h.activeQ = false
		return
	}
	q := h.qs[h.qidx]
	h.answerer = ""
	h.activeQ = true
	h.broadcast(&quizpb.ServerMsg{Payload: &quizpb.ServerMsg_Question{
		Question: &quizpb.Question{Index: int32(h.qidx + 1), Text: q.q},
	}})
}

func (h *hub) onBuzz(pid string) {
	h.mu.Lock()
	if !h.activeQ || h.answerer != "" {
		log.Println("buzz ignored")
		return // 既に誰かが取っている
	}
	p := h.players[pid]
	if p == nil {
		log.Println("buzz from unknown player:", pid)
		return
	}
	h.answerer = pid
	h.mu.Unlock()

	h.broadcast(&quizpb.ServerMsg{Payload: &quizpb.ServerMsg_Buzz{
		Buzz: &quizpb.BuzzResult{PlayerId: pid, Name: p.name},
	}})
}

func (h *hub) onAnswer(pid string, text string) {
	h.mu.Lock()

	if !h.activeQ || pid != h.answerer {
		return // 回答権なし
	}
	ans := strings.TrimSpace(strings.ToLower(text))
	correct := strings.EqualFold(ans, strings.ToLower(h.qs[h.qidx].a))

	// スコア更新
	p := h.players[pid]
	name := "(unknown)"
	if p != nil {
		name = p.name
	}
	if correct {
		h.scores[name]++
	}
	h.mu.Unlock()

	// 裁定を通知
	log.Println("answer judged:", correct)
	h.broadcast(&quizpb.ServerMsg{Payload: &quizpb.ServerMsg_Judge{
		Judge: &quizpb.Judge{
			PlayerId: pid, Correct: correct, Answer: text,
			Comment: fmt.Sprintf("正解: %s", h.qs[h.qidx].a),
		},
	}})
	// スコアボード
	var list []*quizpb.Score
	for n, sc := range h.scores {
		list = append(list, &quizpb.Score{Name: n, Points: int32(sc)})
	}
	h.broadcast(&quizpb.ServerMsg{Payload: &quizpb.ServerMsg_Board{
		Board: &quizpb.Scoreboard{Scores: list},
	}})

	h.qidx++
	h.activeQ = false
	go func() {
		time.Sleep(1200 * time.Millisecond)
		h.nextQuestion()
	}()
}

type quizServer struct {
	quizpb.UnimplementedQuizServer
	h *hub
}

func (s *quizServer) Play(stream quizpb.Quiz_PlayServer) error {
	// 1) プレイヤ生成（まだ hub へは登録しない）
	p := &player{
		id:    fmt.Sprintf("p-%d", time.Now().UnixNano()),
		name:  "anonymous",
		sendC: make(chan *quizpb.ServerMsg, 64),
	}
	// 送信ゴルーチンを先に起動（hub からの個別送信に備える）
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	go func() {
		for {
			select {
			case msg := <-p.sendC:
				if err := stream.Send(msg); err != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// 2) 最初の受信は Join を必須にする → 名前を確定
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if j, ok := first.Payload.(*quizpb.ClientMsg_Join); ok && j.Join != nil {
		p.name = strings.TrimSpace(j.Join.GetName())
	} else {
		// Join 以外が最初に来た場合の扱いはお好みで
		// return status.Errorf(codes.InvalidArgument, "first message must be Join")
	}

	// 3) ここで初めて hub に登録（この時点で名前は yoshi）
	s.h.joinC <- p
	defer func() { s.h.leaveC <- p.id }()

	// 4) ようこそメッセージ（1回だけ、確定名で）
	p.sendC <- &quizpb.ServerMsg{Payload: &quizpb.ServerMsg_Welcome{
		Welcome: &quizpb.Welcome{PlayerId: p.id, Name: p.name},
	}}

	// 5) 残りのメッセージを通常処理
	for {
		in, err := stream.Recv()
		if err != nil {
			return err
		}
		switch in.Payload.(type) {
		case *quizpb.ClientMsg_Join:
			// 以後の Join は無視または名前変更に使う等、方針次第
		}
		in.PlayerId = p.id
		s.h.evC <- clientEvent{from: p.id, msg: in}
	}
}

func main() {
	h := newHub()
	go h.run()

	lis, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatal(err)
	}
	s := grpc.NewServer()
	quizpb.RegisterQuizServer(s, &quizServer{h: h})

	log.Println("Quiz gRPC server listening on :8080")
	if err := s.Serve(lis); err != nil {
		log.Fatal(err)
	}
}
