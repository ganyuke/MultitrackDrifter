export async function api(path, options = {}) {
  const { headers = {}, ...rest } = options;
  const res = await fetch(path, {
    credentials: 'include',
    cache: 'no-store',
    ...rest,
    headers: { 'content-type': 'application/json', ...headers }
  });
  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`;
    try { message = (await res.json()).error || message; } catch (_) {}
    const error = new Error(message);
    error.status = res.status;
    throw error;
  }
  const ct = res.headers.get('content-type') || '';
  return ct.includes('json') ? res.json() : res.text();
}

export const postJSON = (path, body) => api(path, { method: 'POST', body: JSON.stringify(body) });
export const patchJSON = (path, body) => api(path, { method: 'PATCH', body: JSON.stringify(body) });
export const del = (path) => api(path, { method: 'DELETE' });
