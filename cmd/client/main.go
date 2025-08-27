package main

import (
	"context"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	hellopb "grpc-hands-on/gen/grpc"
	"log"
)

func main() {
	fmt.Println("gRPC クライアントを起動するよ...")

	target := "localhost:8080"
	conn, err := grpc.NewClient(
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)

	if err != nil {
		log.Fatal("Connection failed.")
		return
	}
	defer conn.Close()

	client := hellopb.NewGreetingServiceClient(conn)

	for {
		fmt.Print("好きな名前を入力してエンターキーを押してね: ")
		var name string
		fmt.Scan(&name)

		req := &hellopb.HelloRequest{
			Name: name,
		}

		res, err := client.Hello(context.Background(), req)
		if err != nil {
			fmt.Printf("gRPC サーバーから返答がこないよ: %v\n", err)
			break
		}
		fmt.Printf("やった！ gRPC サーバーから返答がきたよ: %s\n\n", res.GetMessage())

	}
}
