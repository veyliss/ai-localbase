import React from 'react'

interface FormInputProps {
  type?: 'text' | 'password' | 'number'
  value: string | number
  onChange: (value: string | number) => void
  placeholder?: string
  disabled?: boolean
  min?: number
  max?: number
  step?: number
}

const FormInput: React.FC<FormInputProps> = ({
  type = 'text',
  value,
  onChange,
  placeholder,
  disabled,
  min,
  max,
  step,
}) => {
  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const val = type === 'number' ? parseFloat(e.target.value) || 0 : e.target.value
    onChange(val)
  }

  return (
    <input
      type={type}
      value={value}
      onChange={handleChange}
      placeholder={placeholder}
      disabled={disabled}
      min={min}
      max={max}
      step={step}
      className="form-input"
    />
  )
}

export default FormInput
