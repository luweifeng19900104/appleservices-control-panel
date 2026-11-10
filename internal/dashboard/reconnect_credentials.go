package dashboard

import (
	"errors"

	"github.com/Laky-64/appleservices/internal/reconnectvault"
)

func (s *Server) reconnectCredentialsAvailable(account *Account) bool {
	return account != nil && s.reconnectVault != nil && s.reconnectVault.Available(account.Dir, account.AppleID)
}

func (s *Server) loadReconnectCredentials(account *Account) (credentials, error) {
	if account == nil || s.reconnectVault == nil {
		return credentials{}, errors.New("没有已保存的重连信息，请输入当前账号密码")
	}
	saved, err := s.reconnectVault.Load(account.Dir, account.AppleID)
	if err != nil {
		return credentials{}, errors.New("已保存的重连信息不可用，请重新输入当前账号密码")
	}
	creds := credentials{AppleID: account.AppleID, Password: saved.Password, DeviceCodeURL: saved.DeviceCodeURL, TwoFactor: saved.TwoFactor}
	if err := normalizeCredentials(&creds); err != nil {
		return credentials{}, errors.New("已保存的重连信息无效，请重新输入当前账号密码")
	}
	return creds, nil
}

func (s *Server) saveReconnectCredentials(account *Account, creds credentials) error {
	if account == nil || s.reconnectVault == nil || creds.Password == "" {
		return nil
	}
	return s.reconnectVault.Save(account.Dir, account.AppleID, reconnectvault.Credentials{
		Password: creds.Password, DeviceCodeURL: creds.DeviceCodeURL, TwoFactor: creds.TwoFactor,
	})
}

func (s *Server) forgetReconnectCredentials(account *Account) error {
	if account == nil || s.reconnectVault == nil {
		return nil
	}
	return s.reconnectVault.Delete(account.Dir)
}
