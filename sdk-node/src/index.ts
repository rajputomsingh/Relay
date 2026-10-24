export interface RelayConfig {
  apiKey: string;
  baseUrl?: string;
}

export interface EventPayload {
  source: string;
  event_type: string;
  data: Record<string, any>;
}

export class Relay {
  private apiKey: string;
  private baseUrl: string;

  constructor(config: RelayConfig) {
    this.apiKey = config.apiKey;
    this.baseUrl = config.baseUrl || 'http://localhost:8080';
  }

  private async request<T>(path: string, options: RequestInit = {}): Promise<T> {
    const headers = {
      'Authorization': `Bearer ${this.apiKey}`,
      'Content-Type': 'application/json',
      ...options.headers,
    };

    const res = await fetch(`${this.baseUrl}${path}`, { ...options, headers });
    
    if (!res.ok) {
      const error = await res.json().catch(() => ({}));
      throw new Error(`Relay API Error: ${res.status} ${error.error || res.statusText}`);
    }
    return res.json();
  }

  async sendEvent(payload: EventPayload, idempotencyKey?: string): Promise<{ event_id: string }> {
    const headers: Record<string, string> = idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : {};
    return this.request('/v1/events', {
      method: 'POST',
      headers,
      body: JSON.stringify(payload),
    });
  }

  async createEndpoint(url: string, options: { event_filter?: any, max_attempts?: number } = {}) {
    return this.request('/v1/endpoints', {
      method: 'POST',
      body: JSON.stringify({ url, ...options }),
    });
  }
}