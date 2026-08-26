package server

import "fuego-hinge/internal/contract"

type Server interface {
	Mount(g any, groups []*contract.Group)
}

func New() *Server {
	panic("该server只是接口规范，请使用已实现的Server或自己实现相关函数")
}
