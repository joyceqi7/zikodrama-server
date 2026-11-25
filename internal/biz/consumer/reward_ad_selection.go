package consumer

import "errors"

// rewardedAdUnitForUID 对十进制业务 UID 逐位取模，避免浮点精度损失和整数溢出。
// 集合顺序来自应用配置的规范化排序；单广告位直接返回，不维护游标或用户分配记录。
func rewardedAdUnitForUID(visitorID string, codes []string) (string, error) {
	if len(codes) == 0 {
		return "", errors.New("reward ad candidate set is empty")
	}
	if len(codes) == 1 {
		return codes[0], nil
	}
	if visitorID == "" {
		return "", errors.New("reward ad allocation requires a decimal visitor ID")
	}
	index := 0
	for _, digit := range visitorID {
		if digit < '0' || digit > '9' {
			return "", errors.New("reward ad allocation requires a decimal visitor ID")
		}
		index = (index*10 + int(digit-'0')) % len(codes)
	}
	return codes[index], nil
}
