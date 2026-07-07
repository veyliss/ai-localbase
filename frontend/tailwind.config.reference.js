/** @type {import('tailwindcss').Config} */
module.exports = {
  // 指定需要 Tailwind 处理的文件范围
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  // 使用 class 模式支持按需切换 Dark 模式
  darkMode: ['class', '[data-theme="dark"]'],
  theme: {
    extend: {
      // --- 1. 字体系统 ---
      fontFamily: {
        sans: ['Inter', '-apple-system', 'BlinkMacSystemFont', 'Segoe UI', 'Roboto', 'Helvetica Neue', 'Arial', 'sans-serif'],
        mono: ['JetBrains Mono', 'SF Mono', 'Menlo', 'monospace'],
      },
      // --- 2. 语义化色彩系统映射 ---
      colors: {
        background: 'var(--bg-app)',
        surface: {
          DEFAULT: 'var(--bg-primary)',
          secondary: 'var(--bg-secondary)',
          tertiary: 'var(--bg-tertiary)',
        },
        primary: {
          DEFAULT: 'var(--color-primary)',
          hover: 'var(--color-primary-hover)',
          active: 'var(--color-primary-active)',
          light: 'var(--color-primary-light)',
          soft: 'var(--color-primary-soft)',
        },
        text: {
          primary: 'var(--text-primary)',
          secondary: 'var(--text-secondary)',
          tertiary: 'var(--text-tertiary)',
        },
        border: {
          DEFAULT: 'var(--border-color)',
          hover: 'var(--border-color-hover)',
          subtle: 'var(--border-subtle)',
        },
        semantic: {
          success: 'var(--color-success)',
          warning: 'var(--color-warning)',
          error: 'var(--color-error)',
          info: 'var(--color-info)',
        }
      },
      // --- 3. 圆角系统映射 ---
      borderRadius: {
        xs: 'var(--radius-xs)',
        sm: 'var(--radius-sm)',
        md: 'var(--radius-md)',
        lg: 'var(--radius-lg)',
        xl: 'var(--radius-xl)',
        '2xl': 'var(--radius-2xl)',
      },
      // --- 4. 阴影系统映射 ---
      boxShadow: {
        xs: 'var(--shadow-xs)',
        sm: 'var(--shadow-sm)',
        md: 'var(--shadow-md)',
        lg: 'var(--shadow-lg)',
        xl: 'var(--shadow-xl)',
        modal: 'var(--shadow-modal)',
        focus: 'var(--shadow-focus)',
      },
      // --- 5. 动效映射 ---
      transitionTimingFunction: {
        'fast': 'cubic-bezier(0.16, 1, 0.3, 1)',
        'normal': 'cubic-bezier(0.16, 1, 0.3, 1)',
        'slow': 'cubic-bezier(0.16, 1, 0.3, 1)',
      },
      transitionDuration: {
        'fast': '150ms',
        'normal': '250ms',
        'slow': '400ms',
      }
    },
  },
  plugins: [
    // 推荐引入如 @tailwindcss/forms 等官方插件进一步统一表单
  ],
}
