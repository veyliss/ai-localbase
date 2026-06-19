import React from 'react'

interface FormFieldProps {
  label: string
  hint?: string
  error?: string
  children: React.ReactNode
}

const FormField: React.FC<FormFieldProps> = ({ label, hint, error, children }) => {
  return (
    <div className="form-group">
      <label className="form-label">{label}</label>
      {hint && <p className="form-hint">{hint}</p>}
      {children}
      {error && <p className="form-error">{error}</p>}
    </div>
  )
}

export default FormField
