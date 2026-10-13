# Test data

Reference values computed by Java Edition, copied from Pumpkin
(github.com/Pumpkin-MC/Pumpkin, `assets/`), which extracted them from the
game:

- `climate_seed0.json`: every 7th row of
  `multi_noise_sample_no_blend_no_beard_0_0_0.json`. Each row is a biome
  (quarter-block) position and the six climate values Java computes there
  for seed 0, times 10000: `[x, y, z, temperature, vegetation,
  continentalness, erosion, depth, weirdness]`.
