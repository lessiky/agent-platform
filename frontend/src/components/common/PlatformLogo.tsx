import { RobotOutlined } from '@ant-design/icons';
import { usePlatformStore } from '@/store/platform-store';

interface PlatformLogoProps {
  // 图标边长 (px)
  size?: number;
  // 是否在图标右侧展示平台名
  withText?: boolean;
  // 文本字号 (px), 默认 size * 0.6
  fontSize?: number;
  // 文本颜色 (默认继承父级颜色)
  textColor?: string;
}

// 平台 Logo: 优先展示自定义图标 (data URL), 未配置时回退内置默认图标
export function PlatformLogo({ size = 32, withText = false, fontSize, textColor }: PlatformLogoProps) {
  const { name, icon } = usePlatformStore();

  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 8,
        minWidth: 0,
        maxWidth: '100%',
      }}
    >
      {icon ? (
        <img
          src={icon}
          alt={name}
          style={{ width: size, height: size, borderRadius: 8, objectFit: 'cover', flexShrink: 0 }}
        />
      ) : (
        <span
          style={{
            width: size,
            height: size,
            borderRadius: 8,
            display: 'inline-flex',
            alignItems: 'center',
            justifyContent: 'center',
            flexShrink: 0,
            background: 'linear-gradient(135deg, #1677ff 0%, #722ed1 100%)',
            color: '#fff',
            fontSize: size * 0.55,
          }}
        >
          <RobotOutlined />
        </span>
      )}
      {withText && (
        <span
          style={{
            fontWeight: 600,
            fontSize: fontSize ?? size * 0.6,
            color: textColor,
            whiteSpace: 'nowrap',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
          }}
        >
          {name}
        </span>
      )}
    </span>
  );
}
