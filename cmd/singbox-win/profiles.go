package main

import (
	"errors"
	"fmt"
	"github.com/JARVIS/sing-box-win/internal/config"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type profileEdit struct {
	Name          *string `json:"name"`
	URL           *string `json:"url"`
	Content       *string `json:"content"`
	AutoUpdate    *bool   `json:"auto_update"`
	UpdateMinutes *int    `json:"update_minutes"`
	Enabled       *bool   `json:"enabled"`
}

func validProfileURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil
}
func (a *App) updateInterval(p Profile) time.Duration {
	if p.URL == "" || (p.AutoUpdate != nil && !*p.AutoUpdate) {
		return 0
	}
	if p.UpdateMinutes > 0 {
		return time.Duration(p.UpdateMinutes) * time.Minute
	}
	if p.AutoUpdate != nil && *p.AutoUpdate {
		return time.Hour
	}
	return time.Duration(a.settings.UpdateHours) * time.Hour
}

// Keep a private recovery copy before replacing configuration bytes.
func (a *App) archiveProfile(p Profile, data []byte, directory string) (string, error) {
	dir := filepath.Join(a.dir, directory)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%d.json", p.ID, time.Now().UnixNano()))
	return path, os.WriteFile(path, data, 0600)
}
func (a *App) replaceProfile(p *Profile, data []byte) error {
	if _, err := config.Parse(data); err != nil {
		return err
	}
	path := a.profilePath(p.ID)
	original, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if _, err = a.archiveProfile(*p, original, "profile-history"); err != nil {
		return err
	}
	running := p.Enabled && a.core != nil
	proxy := a.proxy
	oldUpdated := p.Updated
	if running {
		if err = a.stop(); err != nil {
			return err
		}
	}
	rollback := func(cause error) error {
		writeErr := os.WriteFile(path, original, 0600)
		p.Updated = oldUpdated
		saveErr := a.save()
		var restartErr error
		if running && writeErr == nil {
			restartErr = a.start()
			if restartErr == nil {
				restartErr = a.setProxy(proxy)
			}
		}
		return errors.Join(cause, writeErr, saveErr, restartErr)
	}
	if err = os.WriteFile(path+".tmp", data, 0600); err != nil {
		return rollback(err)
	}
	if err = os.Rename(path+".tmp", path); err != nil {
		return rollback(err)
	}
	p.Updated = time.Now()
	if err = a.save(); err != nil {
		return rollback(err)
	}
	if running {
		if err = a.start(); err != nil {
			return rollback(err)
		}
		if err = a.setProxy(proxy); err != nil {
			_ = a.stop()
			return rollback(err)
		}
	}
	return nil
}
func (a *App) editProfile(idx int, b profileEdit) error {
	old := append([]Profile(nil), a.settings.Profiles...)
	p := &a.settings.Profiles[idx]
	candidate := *p
	if b.Name != nil {
		if strings.TrimSpace(*b.Name) == "" {
			return errors.New("配置名称不能为空")
		}
		candidate.Name = strings.TrimSpace(*b.Name)
	}
	if b.URL != nil {
		value := strings.TrimSpace(*b.URL)
		if value != "" && !validProfileURL(value) {
			return errors.New("订阅 URL 必须是有效的 HTTP/HTTPS 地址")
		}
		if p.Type == "remote" && value == "" {
			return errors.New("远程配置 URL 不能为空")
		}
		if p.Type == "local" && value != "" {
			return errors.New("配置类型不可通过编辑更改，请重新导入远程配置")
		}
		candidate.URL = value
	}
	if b.AutoUpdate != nil {
		candidate.AutoUpdate = b.AutoUpdate
	}
	if b.UpdateMinutes != nil {
		if *b.UpdateMinutes < 1 || *b.UpdateMinutes > 525600 {
			return errors.New("自动更新间隔须为 1–525600 分钟")
		}
		candidate.UpdateMinutes = *b.UpdateMinutes
	}
	if b.Content != nil {
		if _, err := config.Parse([]byte(*b.Content)); err != nil {
			return err
		}
	}
	if b.Enabled != nil {
		if a.core != nil {
			return errors.New("请先停止内核再切换配置")
		}
		if *b.Enabled {
			for i := range a.settings.Profiles {
				a.settings.Profiles[i].Enabled = false
			}
		}
		candidate.Enabled = *b.Enabled
	}
	*p = candidate
	if b.Content != nil {
		if err := a.replaceProfile(p, []byte(*b.Content)); err != nil {
			a.settings.Profiles = old
			_ = a.save()
			return err
		}
	} else if err := a.save(); err != nil {
		a.settings.Profiles = old
		return err
	}
	return nil
}
func (a *App) deleteProfile(idx int) error {
	p := a.settings.Profiles[idx]
	if p.Enabled && a.core != nil {
		return errors.New("请先停止服务再删除当前配置")
	}
	data, err := os.ReadFile(a.profilePath(p.ID))
	if err != nil {
		return err
	}
	if _, err = a.archiveProfile(p, data, "profile-trash"); err != nil {
		return err
	}
	old := append([]Profile(nil), a.settings.Profiles...)
	a.settings.Profiles = append(a.settings.Profiles[:idx], a.settings.Profiles[idx+1:]...)
	if p.Enabled && len(a.settings.Profiles) > 0 {
		a.settings.Profiles[0].Enabled = true
	}
	if err = a.save(); err != nil {
		a.settings.Profiles = old
		return err
	}
	if err = os.Remove(a.profilePath(p.ID)); err != nil {
		a.settings.Profiles = old
		_ = a.save()
		return err
	}
	return nil
}

// Switching profiles is transactional: a bad replacement must not strand a working session.
func (a *App) switchProfile(idx int) error {
	if idx < 0 || idx >= len(a.settings.Profiles) {
		return errors.New("profile not found")
	}
	if a.settings.Profiles[idx].Enabled {
		return nil
	}
	previous := append([]Profile(nil), a.settings.Profiles...)
	running, proxy := a.core != nil, a.proxy
	if running {
		if err := a.stop(); err != nil {
			return err
		}
	}
	for i := range a.settings.Profiles {
		a.settings.Profiles[i].Enabled = i == idx
	}
	err := a.save()
	if err == nil && running {
		err = a.start()
		if err == nil {
			err = a.setProxy(proxy)
		}
	}
	if err == nil {
		return nil
	}
	_ = a.stop()
	a.settings.Profiles = previous
	saveErr := a.save()
	var restoreErr error
	if running {
		restoreErr = a.start()
		if restoreErr == nil {
			restoreErr = a.setProxy(proxy)
		}
	}
	return errors.Join(err, saveErr, restoreErr)
}
