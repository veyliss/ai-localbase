import React from 'react'

interface FormCheckboxProps {
  label: string
  checked: boolean
  onChange: (checked: boolean) => void
  disabled?: boolean
}

const FormCheckbox: React.FC<FormCheckboxProps> = ({ label, checked, onChange, disabled }) => {
  return (
    <label className="form-checkbox">
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        disabled={disabled}
      />
      <span className="checkbox-label">{label}</span>
    </label>
  )
}

export default FormCheckbox
