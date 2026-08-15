export interface SavedSecret {
  id: string
  name: string
  label?: string
  description?: string
  configured?: boolean
  createdAt?: string
  updatedAt?: string
  bindingCount?: number
}

export interface SaveSecretInput {
  id?: string
  name: string
  label?: string
  description?: string
  value?: string
}
