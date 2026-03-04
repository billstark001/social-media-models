# Social Media Models

## Repository Structure

```text
├── root/ 
│   ├── model/              # Agent-based model components
│   │   ├── agent.go        # Agent implementation
│   │   ├── model.go        # Main model logic
│   │   ├── recsys.go       # Recommendation system interface
│   │   └── tweet.go        # Tweet and information sharing
│   ├── recsys/             # Recommendation system implementations
│   │   ├── opinion.go      # Content-based recommendation
│   │   ├── structure.go    # Link-based recommendation
│   │   ├── random.go       # Baseline random recommendation
│   │   └── mix.go          # Hybrid recommendation systems
│   ├── simulation/         # Simulation management and serialization
│   │   ├── scenario.go     # Scenario execution
│   │   ├── event-db.go     # Event logging database
│   │   └── acc-mod-state.go # Accumulative state tracking
│   └── utils/              # Network utilities and graph operations
```

## Model Architecture

### Agent-Based Model Components

The model implements a discrete-time agent-based simulation where:

- **Agents** represent social media users with continuous opinions in [-1, 1]
- **Network** is a directed graph representing follow relationships
- **Tweets** carry opinion information and can be original posts or retweets
- **Recommendation Systems** suggest content to users based on different strategies

### Agent Behavior

Each agent follows these rules at each simulation step:

1. **View Content**: Observe tweets from followed neighbors and recommended content
2. **Opinion Update**: Update opinion based on concordant content (within tolerance threshold ε)
   - Opinion change: Δo = μ × (average of concordant opinions - current opinion)
   - μ: decay/influence parameter
3. **Post/Retweet**: With probability ρ, retweet concordant content; otherwise post new tweet
4. **Rewire**: With probability γ, unfollow discordant neighbor and follow concordant recommended user

### Recommendation Systems

Three main recommendation strategies are implemented:

1. **Random Recommendation** (`Random`): Baseline strategy selecting users randomly
2. **Structure-Based Recommendation** (`StructureM9`): Link-based, recommending based on network proximity
3. **Opinion-Based Recommendation** (`OpinionM9`): Content-based, recommending based on opinion similarity
   - Maintains sorted index of tweets by opinion
   - Recommends content with minimal opinion distance
   - Supports historical tweet retention (parameter: `TweetRetainCount`)

### Key Parameters

- **Tolerance (ε)**: Opinion difference threshold for concordance (default: 0.45)
- **Decay/Influence (μ)**: Opinion update rate (default: 0.05)
- **Rewiring Rate (γ)**: Probability of network rewiring (default: 0.05)
- **Retweet Rate (ρ)**: Probability of retweeting vs. posting (default: 0.3)
- **RecsysCount**: Number of recommendations per agent per step (default: 10)
- **TweetRetainCount**: Number of historical tweets retained (0-6)
