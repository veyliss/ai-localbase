import React from 'react'

interface FormSliderProps {
  value: number
  min: number
  max: number
  step: number
  onChange: (value: number) => void
  disabled?: boolean
}

const FormSlider: React.FC<FormSliderProps> = ({ value, min, max, step, onChange, disabled }) => {
  return (
    <div className="form-slider">
      <input
        type="range"
        min={min}
        max={max}
        step={step}
        value={value}
        onChange={(e) => onChange(parseFloat(e.target.value))}
        disabled={disabled}
        className="slider"
      />
      <span className="slider-value">{value}</span>
    </div>
  )
}

export default FormSlider
