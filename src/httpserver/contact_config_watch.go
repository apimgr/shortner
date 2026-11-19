package httpserver

import (
	"context"
	"os"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
)

func (s *Server) startContactConfigWatcher(path, dbPath string) {
	ctx, cancel := context.WithCancel(context.Background())
	s.contactWatcherCancel = cancel
	s.contactWatcherDone = make(chan struct{})
	go func() {
		defer close(s.contactWatcherDone)
		var lastAttemptMod time.Time
		var lastAttemptSize int64 = -1
		missing := false
		statErrorLogged := false
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			info, err := os.Stat(path)
			if err != nil {
				if os.IsNotExist(err) && !missing {
					loaded, loadErr := config.Load(path, dbPath)
					if loadErr == nil {
						loadErr = config.ValidateContactCaptcha(loaded)
					}
					if loadErr != nil {
						s.logContactConfigReloadError(loadErr)
					} else {
						s.contactDeps.setContactAntispam(loaded.Server.Contact.Antispam)
						missing = true
						lastAttemptMod, lastAttemptSize = time.Time{}, -1
					}
				} else if !os.IsNotExist(err) {
					if !statErrorLogged {
						s.logContactConfigReloadError(err)
						statErrorLogged = true
					}
				}
				continue
			}
			statErrorLogged = false
			missing = false
			if info.ModTime().Equal(lastAttemptMod) && info.Size() == lastAttemptSize {
				continue
			}
			lastAttemptMod, lastAttemptSize = info.ModTime(), info.Size()
			loaded, err := config.Load(path, dbPath)
			if err != nil {
				s.logContactConfigReloadError(err)
				continue
			}
			if err := config.ValidateContactCaptcha(loaded); err != nil {
				s.logContactConfigReloadError(err)
				continue
			}
			s.contactDeps.setContactAntispam(loaded.Server.Contact.Antispam)
		}
	}()
}

func (s *Server) logContactConfigReloadError(_ error) {
	if s.contactDeps == nil || s.contactDeps.audit == nil {
		return
	}
	_ = s.contactDeps.audit.Write(applog.Entry{
		Time: time.Now().UTC(), Event: "config.reload_failed", Category: "system",
		Severity: applog.SeverityWarn,
		Details:  map[string]any{"area": "contact.antispam", "error_category": "read_or_parse"},
		Result:   applog.ResultFailure,
	})
}
