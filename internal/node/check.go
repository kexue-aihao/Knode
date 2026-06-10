package node

import (
	"context"
	"fmt"
	"log"

	"github.com/kexue-aihao/Knode/internal/config"
)

func CheckUpstreams(ctx context.Context, cfg config.Config, logger *log.Logger) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	for _, upstream := range cfg.Upstreams {
		dialer, err := NewUpstreamDialer(upstream)
		if err != nil {
			return fmt.Errorf("upstream %q: %w", upstream.Name, err)
		}
		conn, err := dialer.Dial(ctx)
		if err != nil {
			return fmt.Errorf("upstream %q: %w", upstream.Name, err)
		}
		_ = conn.Close()
		if logger != nil {
			logger.Printf("upstream %s: ok", upstream.Name)
		}
	}
	return nil
}
