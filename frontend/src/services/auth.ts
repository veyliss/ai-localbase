import { parseJsonResponse } from './api'

export const API_BASE_PATH = ''

// Get configured username from backend or default to 'admin'
export const getAuthUsername = async (): Promise<string> => {
  try {
    const response = await fetch(`${API_BASE_PATH}/health`)
    if (response.ok) {
      const data = await parseJsonResponse<{ config?: { authUsername?: string } }>(response)
      return data?.config?.authUsername || 'admin'
    }
  } catch {
    // Ignore errors
  }
  return 'admin'
}
