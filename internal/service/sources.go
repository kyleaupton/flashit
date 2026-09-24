package service

import "github.com/kyleaupton/flashit/internal/sources"

// SourcesService lets the frontend probe an image before a drive is chosen.
type SourcesService struct{}

func NewSourcesService() *SourcesService { return &SourcesService{} }

func (s *SourcesService) Probe(path string) (sources.SourceInfo, error) {
	return sources.Probe(path)
}
