import axios, { AxiosError, type AxiosInstance, type AxiosRequestConfig } from 'axios';

// 后端响应信封
export interface ApiEnvelope<T = unknown> {
  code: string;
  message: string;
  data?: T;
}

// 响应拦截器已返回 response.data (即 ApiEnvelope), 此处显式声明解包后的方法签名,
// 让 api 模块调用时直接得到 Promise<ApiEnvelope<T>>
export interface ApiClient {
  get<T = unknown>(url: string, config?: AxiosRequestConfig): Promise<T>;
  post<T = unknown>(url: string, data?: unknown, config?: AxiosRequestConfig): Promise<T>;
  put<T = unknown>(url: string, data?: unknown, config?: AxiosRequestConfig): Promise<T>;
  patch<T = unknown>(url: string, data?: unknown, config?: AxiosRequestConfig): Promise<T>;
  delete<T = unknown>(url: string, config?: AxiosRequestConfig): Promise<T>;
}

const apiClient: ApiClient & AxiosInstance = axios.create({
  baseURL: import.meta.env.VITE_API_BASE_URL || '/api/v1',
  timeout: 30000,
  headers: {
    'Content-Type': 'application/json',
  },
}) as ApiClient & AxiosInstance;

// 请求拦截器: 附加 JWT
apiClient.interceptors.request.use((config) => {
  const token = localStorage.getItem('access_token');
  if (token) {
    config.headers.Authorization = 'Bearer ' + token;
  }
  return config;
});

// 响应拦截器: 解包信封, 统一 401 处理
apiClient.interceptors.response.use(
  (response) => response.data,
  (error: AxiosError<ApiEnvelope>) => {
    if (error.response?.status === 401) {
      localStorage.removeItem('access_token');
      localStorage.removeItem('username');
      if (window.location.pathname !== '/login') {
        window.location.href = '/login';
      }
    }
    return Promise.reject(error);
  }
);

// 提取后端错误信息
export function getErrorMessage(error: unknown, fallback = '请求失败'): string {
  if (error instanceof AxiosError) {
    const data = error.response?.data;
    if (data && typeof data === 'object' && 'message' in data) {
      return String((data as ApiEnvelope).message);
    }
    if (error.code === 'ECONNABORTED') return '请求超时，请稍后重试';
    return error.message || fallback;
  }
  if (error instanceof Error) return error.message;
  return fallback;
}

export default apiClient;