package gapi

import (
	"fmt"

	db "github.com/tywangq/banking-system/db/sqlc"
	"github.com/tywangq/banking-system/pb"
	"github.com/tywangq/banking-system/token"
	"github.com/tywangq/banking-system/util"
)

type Server struct {
	pb.UnimplementedBankServer
	config     util.Config
	store      db.Store
	tokenMaker token.Maker
}

func NewServer(config util.Config, store db.Store) (*Server, error) {
	tokenMaker, err := token.NewPasetoMaker(config.TokenSymmetricKey)
	if err != nil {
		return nil, fmt.Errorf("cannot create token maker: %w", err)
	}

	server := &Server{
		config:     config,
		store:      store,
		tokenMaker: tokenMaker,
	}

	return server, nil
}
