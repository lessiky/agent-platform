package service

import (
	"context"
	"log"

	"agent-platform/internal/database"
	"agent-platform/internal/middleware"
	"agent-platform/internal/model"
	"agent-platform/internal/repository"
	"agent-platform/pkg/errors"

	"golang.org/x/crypto/bcrypt"

	"gorm.io/gorm"
)

type AuthService interface {
	Login(username, password string) (*model.User, string, error)
	Register(username, email, password string) (*model.User, error)
}

type authService struct {
	repo repository.UserRepository
}

func NewAuthService(repo repository.UserRepository) AuthService {
	return &authService{repo: repo}
}

// Login 用户登录
func (s *authService) Login(username, password string) (*model.User, string, error) {
	ctx := context.Background()

	// 查询用户
	user, err := s.repo.GetByUsername(ctx, username)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, "", errors.ErrUnauthorized
		}
		return nil, "", errors.Wrap(err, "failed to get user")
	}

	// 验证密码
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		return nil, "", errors.ErrUnauthorized
	}

	// 检查状态
	if user.Status != 1 {
		return nil, "", errors.NewValidationError("account is disabled")
	}

	// 获取用户角色
	roles, err := repository.GetUserRoles(database.DB, user.ID)
	if err != nil {
		roles = []string{"user"} // 默认角色
	}

	// 生成 Token
	token, err := middleware.GenerateToken(user.ID, user.Username, roles)
	if err != nil {
		return nil, "", errors.Wrap(err, "failed to generate token")
	}

	return user, token, nil
}

// Register 用户注册
//
// 平台无默认管理员, 采用"首个注册用户自动成为管理员"的上线引导策略:
// 当前不存在持有 admin 角色的活动用户时 (刚上线, 或管理员均被禁用),
// 本次注册用户直接分配 admin 角色 (含全部权限, 无需再叠 user);
// 否则分配默认只读 user 角色。
// 极端并发下 (两个注册请求同时通过检查) 可能产生两个初始管理员,
// 属可接受的低概率事件, 上线后由管理员自行收敛。
func (s *authService) Register(username, email, password string) (*model.User, error) {
	ctx := context.Background()

	// 检查用户名是否已存在
	_, err := s.repo.GetByUsername(ctx, username)
	if err == nil {
		return nil, errors.NewValidationError("username already exists")
	}

	// 加密密码
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, errors.Wrap(err, "failed to hash password")
	}

	// 创建用户
	user := &model.User{
		Username: username,
		Email:    &email,
		Password: string(hashedPassword),
		Status:   1,
	}

	// 创建用户并分配角色: 平台尚无任何活动管理员时, 首个注册用户自动成为 admin
	roleNames := []string{"user"}
	if adminCount, err := s.repo.CountActiveUsersWithRole(ctx, "admin"); err == nil && adminCount == 0 {
		roleNames = []string{"admin"}
		log.Printf("auth: no active admin found, bootstrap first registered user %s as admin", username)
	}
	if err := s.repo.CreateWithRoles(ctx, user, roleNames); err != nil {
		return nil, errors.Wrap(err, "failed to create user")
	}

	return user, nil
}
