import { useState, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { clsx } from "clsx";
import TeamLogo from "../../components/TeamLogo";
import { standingsOptions } from "../../api/queries";
import type { Standing, GroupedStandings } from "../../api/queries";

interface StandingsTabProps {
  leagues: string[];
}

type SportType = "soccer" | "nfl" | "nba" | "nhl" | "mlb" | "other";

interface Column {
  key: string;
  label: string;
  fullName?: string;
  width?: string;
  align?: "left" | "center" | "right";
  getValue: (s: Standing) => React.ReactNode;
}

function getColumnsForSport(sportApi?: string): Column[] {
  const sport = getSportType(sportApi);
  
  const teamCol: Column = {
    key: "team",
    label: "Team",
    fullName: "Team",
    getValue: (s) => (
      <div className="flex items-center gap-2">
        <TeamLogo src={s.team_logo} alt={s.team_name} size="sm" />
        <span className="text-fg-2 font-medium truncate">{s.team_name}</span>
      </div>
    ),
  };

  switch (sport) {
    case "soccer":
      return [
        { key: "rank", label: "#", fullName: "Rank", width: "w-12", align: "center", getValue: (s) => s.rank || "-" },
        { ...teamCol, width: "w-48" },
        { key: "gp", label: "GP", fullName: "Games Played", width: "w-14", align: "center", getValue: (s) => s.games_played },
        { key: "w", label: "W", fullName: "Wins", width: "w-14", align: "center", getValue: (s) => s.wins },
        { key: "d", label: "D", fullName: "Draws", width: "w-14", align: "center", getValue: (s) => s.draws },
        { key: "l", label: "L", fullName: "Losses", width: "w-14", align: "center", getValue: (s) => s.losses },
        { key: "gd", label: "GD", fullName: "Goal Difference", width: "w-16", align: "center", getValue: (s) => s.goal_diff > 0 ? `+${s.goal_diff}` : s.goal_diff },
        { key: "pts", label: "Pts", fullName: "Points", width: "w-16", align: "center", getValue: (s) => s.points },
        { key: "form", label: "Form", fullName: "Recent Form", width: "w-20", getValue: (s) => s.form || "-" },
      ];
    case "nfl":
      return [
        { key: "rank", label: "#", fullName: "Rank", width: "w-12", align: "center", getValue: (s) => s.rank || "-" },
        { ...teamCol, width: "w-48" },
        { key: "w", label: "W", fullName: "Wins", width: "w-14", align: "center", getValue: (s) => s.wins },
        { key: "l", label: "L", fullName: "Losses", width: "w-14", align: "center", getValue: (s) => s.losses },
        { key: "t", label: "T", fullName: "Ties", width: "w-14", align: "center", getValue: (s) => s.draws },
        { key: "pct", label: "Pct", fullName: "Win Percentage", width: "w-16", align: "center", getValue: (s) => s.pct || "-" },
        { key: "pf", label: "PF", fullName: "Points For", width: "w-16", align: "center", getValue: (s) => s.points_for || "-" },
        { key: "pa", label: "PA", fullName: "Points Against", width: "w-16", align: "center", getValue: (s) => s.points_against || "-" },
        { key: "streak", label: "Str", fullName: "Streak", width: "w-16", getValue: (s) => s.streak || "-" },
      ];
    case "nba":
    case "mlb":
      return [
        { key: "rank", label: "#", fullName: "Rank", width: "w-12", align: "center", getValue: (s) => s.rank || "-" },
        { ...teamCol, width: "w-48" },
        { key: "w", label: "W", fullName: "Wins", width: "w-14", align: "center", getValue: (s) => s.wins },
        { key: "l", label: "L", fullName: "Losses", width: "w-14", align: "center", getValue: (s) => s.losses },
        { key: "pct", label: "Pct", fullName: "Win Percentage", width: "w-16", align: "center", getValue: (s) => s.pct || "-" },
        { key: "gb", label: "GB", fullName: "Games Behind", width: "w-16", align: "center", getValue: (s) => s.games_behind || "-" },
        { key: "pf", label: "PF", fullName: "Points For", width: "w-16", align: "center", getValue: (s) => s.points_for || "-" },
        { key: "pa", label: "PA", fullName: "Points Against", width: "w-16", align: "center", getValue: (s) => s.points_against || "-" },
        { key: "streak", label: "Str", fullName: "Streak", width: "w-16", getValue: (s) => s.streak || "-" },
      ];
    case "nhl":
      return [
        { key: "rank", label: "#", fullName: "Rank", width: "w-12", align: "center", getValue: (s) => s.rank || "-" },
        { ...teamCol, width: "w-48" },
        { key: "gp", label: "GP", fullName: "Games Played", width: "w-14", align: "center", getValue: (s) => s.games_played },
        { key: "w", label: "W", fullName: "Wins", width: "w-14", align: "center", getValue: (s) => s.wins },
        { key: "l", label: "L", fullName: "Losses", width: "w-14", align: "center", getValue: (s) => s.losses },
        { key: "otl", label: "OTL", fullName: "Overtime Losses", width: "w-14", align: "center", getValue: (s) => s.otl ?? "-" },
        { key: "pts", label: "Pts", fullName: "Points", width: "w-16", align: "center", getValue: (s) => s.points },
        { key: "gf", label: "GF", fullName: "Goals For", width: "w-16", align: "center", getValue: (s) => s.goals_for ?? "-" },
        { key: "ga", label: "GA", fullName: "Goals Against", width: "w-16", align: "center", getValue: (s) => s.goals_against ?? "-" },
        { key: "streak", label: "Str", fullName: "Streak", width: "w-16", getValue: (s) => s.streak || "-" },
      ];
    default:
      return [
        { key: "rank", label: "#", fullName: "Rank", width: "w-12", align: "center", getValue: (s) => s.rank || "-" },
        { ...teamCol, width: "w-48" },
        { key: "gp", label: "GP", fullName: "Games Played", width: "w-14", align: "center", getValue: (s) => s.games_played },
        { key: "w", label: "W", fullName: "Wins", width: "w-14", align: "center", getValue: (s) => s.wins },
        { key: "l", label: "L", fullName: "Losses", width: "w-14", align: "center", getValue: (s) => s.losses },
        { key: "d", label: "D", fullName: "Draws", width: "w-14", align: "center", getValue: (s) => s.draws },
        { key: "pts", label: "Pts", fullName: "Points", width: "w-16", align: "center", getValue: (s) => s.points },
      ];
  }
}

function getSportType(sportApi?: string): SportType {
  if (!sportApi) return "soccer";
  if (sportApi === "american-football") return "nfl";
  if (sportApi === "basketball") return "nba";
  if (sportApi === "hockey") return "nhl";
  if (sportApi === "baseball") return "mlb";
  if (sportApi === "football") return "soccer";
  return "other";
}

// Chevron icon for collapsible sections
function ChevronIcon({ expanded }: { expanded: boolean }) {
  return (
    <svg
      className={clsx(
        "w-4 h-4 transition-transform duration-200",
        expanded && "rotate-90"
      )}
      fill="none"
      viewBox="0 0 24 24"
      stroke="currentColor"
    >
      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" />
    </svg>
  );
}

interface DivisionSectionProps {
  divisionName: string;
  standings: Standing[];
  columns: Column[];
  defaultExpanded?: boolean;
}

function DivisionSection({ divisionName, standings, columns, defaultExpanded = true }: DivisionSectionProps) {
  const [expanded, setExpanded] = useState(defaultExpanded);

  // Get leader record for collapsed summary
  const leader = standings[0];
  const leaderRecord = leader ? `${leader.wins}-${leader.losses}` : "";

  return (
    <div className="border-b border-edge last:border-b-0">
      {/* Collapsible header */}
      <button
        onClick={() => setExpanded(!expanded)}
        className="w-full flex items-center justify-between px-3 py-2 bg-surface-hover hover:bg-surface-active transition-colors text-left"
      >
        <div className="flex items-center gap-2">
          <ChevronIcon expanded={expanded} />
          <span className="text-xs font-semibold text-fg-2">{divisionName}</span>
        </div>
        {!expanded && leader && (
          <span className="text-[10px] text-fg-4">
            {leader.team_name} ({leaderRecord})
          </span>
        )}
      </button>

      {/* Expanded content */}
      {expanded && (
        <table className="w-full text-xs table-fixed">
          <thead>
            <tr className="text-fg-4 text-[10px] uppercase tracking-wider border-b border-edge">
              {columns.map((col) => (
                <th
                  key={col.key}
                  title={col.fullName || col.label}
                  className={clsx(
                    "px-2 py-2",
                    col.width,
                    col.align === "center" && "text-center",
                    col.align === "right" && "text-right",
                    !col.align && "text-left"
                  )}
                >
                  {col.label}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {standings.map((s, i) => (
              <tr
                key={`${s.team_name}-${i}`}
                className="border-b border-edge/50 hover:bg-surface-hover transition-colors"
              >
                {columns.map((col) => (
                  <td
                    key={col.key}
                    className={clsx(
                      "px-2 py-1.5",
                      col.width,
                      col.key !== "team" && "font-mono text-fg-2",
                      col.align === "center" && "text-center",
                      col.align === "right" && "text-right",
                      !col.align && "text-left"
                    )}
                  >
                    {col.getValue(s)}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

// Define division order for MLB
const MLB_DIVISION_ORDER = [
  "AL - East",
  "AL - Central", 
  "AL - West",
  "NL - East",
  "NL - Central",
  "NL - West",
];

export function StandingsTab({ leagues }: StandingsTabProps) {
  const [selected, setSelected] = useState(leagues[0] ?? "");

  const { data, isLoading, isError } = useQuery({
    ...standingsOptions(selected),
    enabled: !!selected,
  });

  const groupedStandings: GroupedStandings = data?.standings ?? {};

  // Sort divisions in a logical order (AL then NL, East to West)
  const sortedDivisions = useMemo(() => {
    const divisions = Object.keys(groupedStandings);
    
    // For MLB, use predefined order
    if (selected === "MLB") {
      return MLB_DIVISION_ORDER.filter(d => divisions.includes(d));
    }
    
    // For other leagues, sort alphabetically but put "Ungrouped" last
    return divisions.sort((a, b) => {
      if (a === "Ungrouped") return 1;
      if (b === "Ungrouped") return -1;
      return a.localeCompare(b);
    });
  }, [groupedStandings, selected]);

  // Get columns based on first team's sport_api
  const columns = useMemo(() => {
    const firstDivision = sortedDivisions[0];
    const firstTeam = firstDivision ? groupedStandings[firstDivision]?.[0] : undefined;
    return getColumnsForSport(firstTeam?.sport_api);
  }, [groupedStandings, sortedDivisions]);

  const hasStandings = sortedDivisions.length > 0;

  if (leagues.length === 0) {
    return (
      <div className="flex items-center justify-center py-12 text-fg-4 text-xs">
        Add leagues in the Configure tab to see standings
      </div>
    );
  }

  return (
    <div>
      {/* League selector */}
      <div className="px-3 py-2 border-b border-edge bg-surface">
        <select
          value={selected}
          onChange={(e) => setSelected(e.target.value)}
          className="bg-surface-hover text-fg-2 text-xs rounded px-2 py-1 border border-edge focus:outline-none focus:border-primary"
        >
          {leagues.map((l) => (
            <option key={l} value={l}>{l}</option>
          ))}
        </select>
      </div>

      {/* Content */}
      {isLoading && (
        <div className="flex items-center justify-center py-12 text-fg-4 text-xs">
          Loading standings...
        </div>
      )}

      {isError && (
        <div className="flex items-center justify-center py-12 text-error text-xs">
          Failed to load standings
        </div>
      )}

      {!isLoading && !isError && !hasStandings && (
        <div className="flex items-center justify-center py-12 text-fg-4 text-xs">
          No standings available for {selected}
        </div>
      )}

      {hasStandings && (
        <div className="overflow-x-auto">
          {sortedDivisions.map((division) => (
            <DivisionSection
              key={division}
              divisionName={division}
              standings={groupedStandings[division]}
              columns={columns}
              defaultExpanded={true}
            />
          ))}
        </div>
      )}
    </div>
  );
}
