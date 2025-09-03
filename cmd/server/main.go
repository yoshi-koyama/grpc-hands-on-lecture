package main

import (
	"context"
	"errors"
	"fmt"
	pb "grpc-hands-on/gen/grpc"
	"io"
	"log"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	port := 8080
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	server := grpc.NewServer()
	pb.RegisterGreetingServiceServer(server, NewMyServer())

	reflection.Register(server)

	if err := server.Serve(listener); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

type myServer struct {
	pb.UnimplementedGreetingServiceServer
}

func NewMyServer() *myServer {
	return &myServer{}
}

func (s *myServer) Hello(ctx context.Context, req *pb.HelloRequest) (*pb.HelloResponse, error) {
	log.Printf("received: %v", req.GetName())
	return &pb.HelloResponse{
		Message: fmt.Sprintf("Hello, %s!", req.GetName()),
	}, nil
}

func (s *myServer) HelloServerStream(req *pb.HelloRequest, stream pb.GreetingService_HelloServerStreamServer) error {
	resCount := 5
	for i := 0; i < resCount; i++ {
		if err := stream.Send(&pb.HelloResponse{
			Message: fmt.Sprintf("[%d] Hello, %s!", i, req.GetName()),
		}); err != nil {
			log.Println("error occurred")
			return err
		}
		time.Sleep(time.Second * 1)
	}
	return nil
}

func (s *myServer) HelloClientStream(stream pb.GreetingService_HelloClientStreamServer) error {
	nameList := make([]string, 0)
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			message := fmt.Sprintf("Hello, %v!", nameList)
			return stream.SendAndClose(&pb.HelloResponse{
				Message: message,
			})
		}
		if err != nil {
			return err
		}
		nameList = append(nameList, req.GetName())
	}
}

func (s *myServer) HelloBiStreams(stream pb.GreetingService_HelloBiStreamsServer) error {
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		message := fmt.Sprintf("Hello, %v!", req.GetName())
		if err := stream.Send(&pb.HelloResponse{
			Message: message,
		}); err != nil {
			return err
		}
	}
}
