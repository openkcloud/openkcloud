"""Fixture BundleProfiles from manual AB_compare (T2 all_6_valid)."""

try:
    from .selector import BundleProfile
except ImportError:  # python path = this directory
    from selector import BundleProfile

# Median Wh; parallelism tag documents observed CNN Pending during LLM.
FIXTURE_PROFILES = {
    "A_gpu_serial": BundleProfile(
        config_id="A_gpu_serial",
        predicted_server_wh=136.0,
        sla_pass=True,
        confidence="MEASURED",
        profile_id="fixture:ab_compare:A_median",
        notes="Wh 136/140/135; default-scheduler; human-pinned devices",
    ),
    "B_rngd_gpu": BundleProfile(
        config_id="B_rngd_gpu",
        predicted_server_wh=88.0,
        sla_pass=True,
        confidence="MEASURED",
        profile_id="fixture:ab_compare:B_median",
        notes=(
            "Wh 88/88/87; observed_serial: CNN Pending while LLM Running "
            "(pods_t8s); not true parallel overlap profile"
        ),
    ),
}
