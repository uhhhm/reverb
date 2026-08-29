import { API_BASE } from './api'

export interface UploadResult {
  uploaded: { name: string; bytes: number }[]
  rejected?: Record<string, string>
  scanning: boolean
}

/** Formats Reverb accepts for upload; also used for the file input's accept attribute. */
export const UPLOAD_EXTENSIONS = ['.mp3', '.flac', '.m4a', '.aac', '.ogg', '.wav']

/**
 * Uploads audio files into the managed music directory.
 *
 * Uses XHR rather than fetch so the caller can show real upload progress —
 * fetch has no upload-progress event, and an audio file is large enough that a
 * bare spinner would leave the user guessing.
 */
export function uploadTracks(
  files: File[],
  onProgress?: (fraction: number) => void,
): Promise<UploadResult> {
  const form = new FormData()
  for (const f of files) form.append('files', f, f.name)

  return new Promise<UploadResult>((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', `${API_BASE}/library/upload`)
    xhr.withCredentials = true
    if (onProgress) {
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable) onProgress(e.loaded / e.total)
      }
    }
    xhr.onload = () => {
      let body: UploadResult | { error?: string } | null
      try {
        body = JSON.parse(xhr.responseText) as UploadResult | { error?: string }
      } catch {
        body = null
      }
      if (xhr.status >= 200 && xhr.status < 300 && body && 'uploaded' in body) {
        resolve(body)
        return
      }
      const err = body && 'error' in body ? body.error : undefined
      reject(new Error(err || `Upload failed (${xhr.status})`))
    }
    xhr.onerror = () => reject(new Error('Upload failed'))
    xhr.send(form)
  })
}
