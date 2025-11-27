"""
IPMI Collector Service - Physical hardware sensor data collection.

This module provides functions to collect IPMI sensor data from Prometheus.
Data source: IPMI Exporter (https://github.com/prometheus-community/ipmi_exporter)

Expected Prometheus Metrics:
- ipmi_power_watts: System power consumption
- ipmi_temperature_celsius: Temperature sensors
- ipmi_fan_speed_rpm: Fan speed sensors
- ipmi_voltage_volts: Voltage rail sensors
- ipmi_current_amperes: Current sensors
- ipmi_sensor_state: Sensor status (0=nominal, 1=warning, 2=critical)

Note: IPMI Exporter must be configured and running on target nodes.
Configuration example in spec/PROMETHEUS_SETUP.md
"""

from datetime import datetime
from typing import List, Dict, Any, Optional
import logging

from app.models.hardware.ipmi import (
    IPMISensorData,
    IPMIPowerData,
    IPMITemperatureData,
    IPMIFanData,
    IPMIVoltageData,
    IPMISensorType,
    IPMISensorStatus
)
from app.services.prometheus import PrometheusClient, PrometheusException

logger = logging.getLogger(__name__)


class IPMICollector:
    """Collector for IPMI sensor data from Prometheus."""

    def __init__(self, prometheus_client: PrometheusClient):
        """
        Initialize IPMI collector.

        Args:
            prometheus_client: Prometheus client instance
        """
        self.prom = prometheus_client

    def _determine_sensor_status(
        self,
        value: float,
        lower_critical: Optional[float] = None,
        lower_warning: Optional[float] = None,
        upper_warning: Optional[float] = None,
        upper_critical: Optional[float] = None
    ) -> IPMISensorStatus:
        """
        Determine sensor status based on value and thresholds.

        Args:
            value: Current sensor value
            lower_critical: Lower critical threshold
            lower_warning: Lower warning threshold
            upper_warning: Upper warning threshold
            upper_critical: Upper critical threshold

        Returns:
            IPMISensorStatus enum value
        """
        if lower_critical is not None and value <= lower_critical:
            return IPMISensorStatus.CRITICAL
        if upper_critical is not None and value >= upper_critical:
            return IPMISensorStatus.CRITICAL
        if lower_warning is not None and value <= lower_warning:
            return IPMISensorStatus.WARNING
        if upper_warning is not None and value >= upper_warning:
            return IPMISensorStatus.WARNING

        return IPMISensorStatus.NORMAL

    def _parse_ipmi_metrics(
        self,
        result: List[Dict[str, Any]],
        sensor_type: IPMISensorType,
        unit: str
    ) -> List[IPMISensorData]:
        """
        Parse IPMI metrics from Prometheus result.

        Args:
            result: Prometheus query result
            sensor_type: Type of sensor
            unit: Measurement unit

        Returns:
            List of IPMISensorData objects
        """
        sensors = []
        timestamp = datetime.utcnow()

        for metric in result:
            labels = metric.get('metric', {})
            value = float(metric.get('value', [0, '0'])[1])

            # Extract labels
            node_name = labels.get('instance', 'unknown').split(':')[0]
            sensor_name = labels.get('sensor', labels.get('name', 'unknown'))
            sensor_id = labels.get('id', sensor_name)
            entity_id = labels.get('entity_id')

            # Get thresholds if available
            lower_critical = labels.get('lower_critical')
            lower_warning = labels.get('lower_warning')
            upper_warning = labels.get('upper_warning')
            upper_critical = labels.get('upper_critical')

            # Convert string thresholds to float
            if lower_critical:
                lower_critical = float(lower_critical)
            if lower_warning:
                lower_warning = float(lower_warning)
            if upper_warning:
                upper_warning = float(upper_warning)
            if upper_critical:
                upper_critical = float(upper_critical)

            # Determine status
            status = self._determine_sensor_status(
                value, lower_critical, lower_warning, upper_warning, upper_critical
            )

            sensors.append(IPMISensorData(
                sensor_id=sensor_id,
                sensor_name=sensor_name,
                sensor_type=sensor_type,
                node_name=node_name,
                value=value,
                unit=unit,
                status=status,
                lower_critical=lower_critical,
                lower_warning=lower_warning,
                upper_warning=upper_warning,
                upper_critical=upper_critical,
                timestamp=timestamp,
                entity_id=entity_id
            ))

        return sensors

    async def get_all_sensors(
        self,
        node_filter: Optional[str] = None,
        sensor_type_filter: Optional[IPMISensorType] = None
    ) -> List[IPMISensorData]:
        """
        Get all IPMI sensors from Prometheus.

        Args:
            node_filter: Filter by node name
            sensor_type_filter: Filter by sensor type

        Returns:
            List of all IPMI sensors

        Raises:
            PrometheusException: If query fails
        """
        all_sensors = []

        # Query different sensor types based on filter
        sensor_queries = {}

        if sensor_type_filter is None or sensor_type_filter == IPMISensorType.TEMPERATURE:
            sensor_queries['temperature'] = ('ipmi_temperature_celsius', 'celsius')

        if sensor_type_filter is None or sensor_type_filter == IPMISensorType.POWER:
            sensor_queries['power'] = ('ipmi_power_watts', 'watts')

        if sensor_type_filter is None or sensor_type_filter == IPMISensorType.FAN:
            sensor_queries['fan'] = ('ipmi_fan_speed_rpm', 'rpm')

        if sensor_type_filter is None or sensor_type_filter == IPMISensorType.VOLTAGE:
            sensor_queries['voltage'] = ('ipmi_voltage_volts', 'volts')

        if sensor_type_filter is None or sensor_type_filter == IPMISensorType.CURRENT:
            sensor_queries['current'] = ('ipmi_current_amperes', 'amperes')

        # Execute queries
        for sensor_name, (metric, unit) in sensor_queries.items():
            try:
                # Build query with optional node filter
                if node_filter:
                    query = f'{metric}{{instance=~"{node_filter}:.*"}}'
                else:
                    query = metric

                result = self.prom.query(query)
                metrics = result.get('data', {}).get('result', [])

                if metrics:
                    sensor_type = IPMISensorType(sensor_name)
                    sensors = self._parse_ipmi_metrics(metrics, sensor_type, unit)
                    all_sensors.extend(sensors)

            except PrometheusException as e:
                logger.warning(f"Failed to query {metric}: {e}")
                continue

        return all_sensors

    async def get_power_data(
        self,
        node_filter: Optional[str] = None
    ) -> List[IPMIPowerData]:
        """
        Get IPMI power sensor data.

        Args:
            node_filter: Filter by node name

        Returns:
            List of IPMIPowerData per node

        Raises:
            PrometheusException: If query fails
        """
        # Query power metrics
        if node_filter:
            query = f'ipmi_power_watts{{instance=~"{node_filter}:.*"}}'
        else:
            query = 'ipmi_power_watts'

        try:
            result = self.prom.query(query)
            metrics = result.get('data', {}).get('result', [])
        except PrometheusException as e:
            logger.error(f"Failed to query IPMI power data: {e}")
            return []

        # Group by node
        node_power: Dict[str, Dict[str, Any]] = {}
        timestamp = datetime.utcnow()

        for metric in metrics:
            labels = metric.get('metric', {})
            value = float(metric.get('value', [0, '0'])[1])

            node_name = labels.get('instance', 'unknown').split(':')[0]
            sensor_name = labels.get('sensor', labels.get('name', 'unknown'))

            if node_name not in node_power:
                node_power[node_name] = {
                    'total': 0,
                    'psu1': None,
                    'psu2': None,
                    'cpu': None,
                    'memory': None,
                    'status': IPMISensorStatus.NORMAL
                }

            # Map sensor names to fields
            sensor_lower = sensor_name.lower()
            if 'psu1' in sensor_lower or 'ps1' in sensor_lower:
                node_power[node_name]['psu1'] = value
            elif 'psu2' in sensor_lower or 'ps2' in sensor_lower:
                node_power[node_name]['psu2'] = value
            elif 'cpu' in sensor_lower:
                node_power[node_name]['cpu'] = value
            elif 'mem' in sensor_lower or 'dimm' in sensor_lower:
                node_power[node_name]['memory'] = value
            elif 'total' in sensor_lower or 'system' in sensor_lower:
                node_power[node_name]['total'] = value
            else:
                # Add to total if not categorized
                node_power[node_name]['total'] += value

        # Convert to IPMIPowerData objects
        power_data_list = []
        for node_name, data in node_power.items():
            power_data_list.append(IPMIPowerData(
                node_name=node_name,
                timestamp=timestamp,
                total_power_watts=data['total'],
                psu1_power_watts=data['psu1'],
                psu2_power_watts=data['psu2'],
                cpu_power_watts=data['cpu'],
                memory_power_watts=data['memory'],
                power_status=data['status']
            ))

        return power_data_list

    async def get_temperature_data(
        self,
        node_filter: Optional[str] = None
    ) -> List[IPMITemperatureData]:
        """
        Get IPMI temperature sensor data.

        Args:
            node_filter: Filter by node name

        Returns:
            List of IPMITemperatureData per node

        Raises:
            PrometheusException: If query fails
        """
        # Query temperature metrics
        if node_filter:
            query = f'ipmi_temperature_celsius{{instance=~"{node_filter}:.*"}}'
        else:
            query = 'ipmi_temperature_celsius'

        try:
            result = self.prom.query(query)
            metrics = result.get('data', {}).get('result', [])
        except PrometheusException as e:
            logger.error(f"Failed to query IPMI temperature data: {e}")
            return []

        # Group by node
        node_temps: Dict[str, Dict[str, Any]] = {}
        timestamp = datetime.utcnow()

        for metric in metrics:
            labels = metric.get('metric', {})
            value = float(metric.get('value', [0, '0'])[1])

            node_name = labels.get('instance', 'unknown').split(':')[0]
            sensor_name = labels.get('sensor', labels.get('name', 'unknown'))

            if node_name not in node_temps:
                node_temps[node_name] = {
                    'cpu1': None,
                    'cpu2': None,
                    'inlet': None,
                    'exhaust': None,
                    'motherboard': None,
                    'memory': None,
                    'pcie': None,
                    'highest': 0,
                    'critical_count': 0,
                    'warning_count': 0,
                    'status': IPMISensorStatus.NORMAL
                }

            # Map sensor names to fields
            sensor_lower = sensor_name.lower()
            if 'cpu1' in sensor_lower or 'cpu 1' in sensor_lower:
                node_temps[node_name]['cpu1'] = value
            elif 'cpu2' in sensor_lower or 'cpu 2' in sensor_lower:
                node_temps[node_name]['cpu2'] = value
            elif 'inlet' in sensor_lower or 'front' in sensor_lower:
                node_temps[node_name]['inlet'] = value
            elif 'exhaust' in sensor_lower or 'rear' in sensor_lower:
                node_temps[node_name]['exhaust'] = value
            elif 'system' in sensor_lower or 'board' in sensor_lower:
                node_temps[node_name]['motherboard'] = value
            elif 'mem' in sensor_lower or 'dimm' in sensor_lower:
                node_temps[node_name]['memory'] = value
            elif 'pcie' in sensor_lower or 'pci' in sensor_lower:
                node_temps[node_name]['pcie'] = value

            # Track highest temperature
            if value > node_temps[node_name]['highest']:
                node_temps[node_name]['highest'] = value

            # Check status (common thresholds)
            upper_critical = labels.get('upper_critical')
            upper_warning = labels.get('upper_warning')

            if upper_critical and value >= float(upper_critical):
                node_temps[node_name]['critical_count'] += 1
                node_temps[node_name]['status'] = IPMISensorStatus.CRITICAL
            elif upper_warning and value >= float(upper_warning):
                node_temps[node_name]['warning_count'] += 1
                if node_temps[node_name]['status'] != IPMISensorStatus.CRITICAL:
                    node_temps[node_name]['status'] = IPMISensorStatus.WARNING

        # Convert to IPMITemperatureData objects
        temp_data_list = []
        for node_name, data in node_temps.items():
            temp_data_list.append(IPMITemperatureData(
                node_name=node_name,
                timestamp=timestamp,
                cpu1_temperature_celsius=data['cpu1'],
                cpu2_temperature_celsius=data['cpu2'],
                inlet_temperature_celsius=data['inlet'],
                exhaust_temperature_celsius=data['exhaust'],
                motherboard_temperature_celsius=data['motherboard'],
                memory_temperature_celsius=data['memory'],
                pcie_temperature_celsius=data['pcie'],
                overall_temperature_status=data['status'],
                highest_temperature_celsius=data['highest'] if data['highest'] > 0 else None,
                critical_temperature_count=data['critical_count'],
                warning_temperature_count=data['warning_count']
            ))

        return temp_data_list

    async def get_fan_data(
        self,
        node_filter: Optional[str] = None
    ) -> List[IPMIFanData]:
        """
        Get IPMI fan sensor data.

        Args:
            node_filter: Filter by node name

        Returns:
            List of IPMIFanData per node

        Raises:
            PrometheusException: If query fails
        """
        # Query fan metrics
        if node_filter:
            query = f'ipmi_fan_speed_rpm{{instance=~"{node_filter}:.*"}}'
        else:
            query = 'ipmi_fan_speed_rpm'

        try:
            result = self.prom.query(query)
            metrics = result.get('data', {}).get('result', [])
        except PrometheusException as e:
            logger.error(f"Failed to query IPMI fan data: {e}")
            return []

        # Group by node
        node_fans: Dict[str, Dict[str, Any]] = {}
        timestamp = datetime.utcnow()

        for metric in metrics:
            labels = metric.get('metric', {})
            value = float(metric.get('value', [0, '0'])[1])

            node_name = labels.get('instance', 'unknown').split(':')[0]
            sensor_name = labels.get('sensor', labels.get('name', 'unknown'))

            if node_name not in node_fans:
                node_fans[node_name] = {
                    'fan1': None,
                    'fan2': None,
                    'fan3': None,
                    'fan4': None,
                    'fan5': None,
                    'fan6': None,
                    'total_speed': 0,
                    'fan_count': 0,
                    'failure_count': 0,
                    'status': IPMISensorStatus.NORMAL
                }

            # Map sensor names to fields
            sensor_lower = sensor_name.lower()
            fan_num = None
            for i in range(1, 7):
                if f'fan{i}' in sensor_lower or f'fan {i}' in sensor_lower:
                    fan_num = i
                    break

            if fan_num:
                node_fans[node_name][f'fan{fan_num}'] = int(value)

            # Track average
            node_fans[node_name]['total_speed'] += value
            node_fans[node_name]['fan_count'] += 1

            # Check for failures (RPM < 100 typically means failure)
            if value < 100:
                node_fans[node_name]['failure_count'] += 1
                node_fans[node_name]['status'] = IPMISensorStatus.CRITICAL

        # Convert to IPMIFanData objects
        fan_data_list = []
        for node_name, data in node_fans.items():
            avg_speed = data['total_speed'] / data['fan_count'] if data['fan_count'] > 0 else None

            fan_data_list.append(IPMIFanData(
                node_name=node_name,
                timestamp=timestamp,
                fan1_speed_rpm=data['fan1'],
                fan2_speed_rpm=data['fan2'],
                fan3_speed_rpm=data['fan3'],
                fan4_speed_rpm=data['fan4'],
                fan5_speed_rpm=data['fan5'],
                fan6_speed_rpm=data['fan6'],
                overall_fan_status=data['status'],
                fan_failure_count=data['failure_count'],
                avg_fan_speed_rpm=avg_speed
            ))

        return fan_data_list

    async def get_voltage_data(
        self,
        node_filter: Optional[str] = None
    ) -> List[IPMIVoltageData]:
        """
        Get IPMI voltage sensor data.

        Args:
            node_filter: Filter by node name

        Returns:
            List of IPMIVoltageData per node

        Raises:
            PrometheusException: If query fails
        """
        # Query voltage metrics
        if node_filter:
            query = f'ipmi_voltage_volts{{instance=~"{node_filter}:.*"}}'
        else:
            query = 'ipmi_voltage_volts'

        try:
            result = self.prom.query(query)
            metrics = result.get('data', {}).get('result', [])
        except PrometheusException as e:
            logger.error(f"Failed to query IPMI voltage data: {e}")
            return []

        # Group by node
        node_voltages: Dict[str, Dict[str, Any]] = {}
        timestamp = datetime.utcnow()

        for metric in metrics:
            labels = metric.get('metric', {})
            value = float(metric.get('value', [0, '0'])[1])

            node_name = labels.get('instance', 'unknown').split(':')[0]
            sensor_name = labels.get('sensor', labels.get('name', 'unknown'))

            if node_name not in node_voltages:
                node_voltages[node_name] = {
                    '12v': None,
                    '5v': None,
                    '3_3v': None,
                    'cpu': None,
                    'dimm': None,
                    'warning_count': 0,
                    'status': IPMISensorStatus.NORMAL
                }

            # Map sensor names to fields
            sensor_lower = sensor_name.lower()
            if '12v' in sensor_lower or '12 v' in sensor_lower:
                node_voltages[node_name]['12v'] = value
            elif '5v' in sensor_lower or '5 v' in sensor_lower:
                node_voltages[node_name]['5v'] = value
            elif '3.3v' in sensor_lower or '3v3' in sensor_lower:
                node_voltages[node_name]['3_3v'] = value
            elif 'cpu' in sensor_lower or 'vcore' in sensor_lower:
                node_voltages[node_name]['cpu'] = value
            elif 'dimm' in sensor_lower or 'mem' in sensor_lower:
                node_voltages[node_name]['dimm'] = value

            # Check status
            upper_critical = labels.get('upper_critical')
            lower_critical = labels.get('lower_critical')
            upper_warning = labels.get('upper_warning')
            lower_warning = labels.get('lower_warning')

            is_critical = False
            is_warning = False

            if upper_critical and value >= float(upper_critical):
                is_critical = True
            elif lower_critical and value <= float(lower_critical):
                is_critical = True
            elif upper_warning and value >= float(upper_warning):
                is_warning = True
            elif lower_warning and value <= float(lower_warning):
                is_warning = True

            if is_critical:
                node_voltages[node_name]['status'] = IPMISensorStatus.CRITICAL
            elif is_warning:
                node_voltages[node_name]['warning_count'] += 1
                if node_voltages[node_name]['status'] != IPMISensorStatus.CRITICAL:
                    node_voltages[node_name]['status'] = IPMISensorStatus.WARNING

        # Convert to IPMIVoltageData objects
        voltage_data_list = []
        for node_name, data in node_voltages.items():
            voltage_data_list.append(IPMIVoltageData(
                node_name=node_name,
                timestamp=timestamp,
                voltage_12v=data['12v'],
                voltage_5v=data['5v'],
                voltage_3_3v=data['3_3v'],
                voltage_cpu=data['cpu'],
                voltage_dimm=data['dimm'],
                overall_voltage_status=data['status'],
                voltage_warning_count=data['warning_count']
            ))

        return voltage_data_list
